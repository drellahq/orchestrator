package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/drellabot/orchestrator/internal/task"
	"github.com/drellabot/orchestrator/internal/tasksource"
	"github.com/drellabot/orchestrator/internal/vcs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPRemotePort is the port exposed inside the sandbox VM. The MCP server
// itself listens on a dynamic port on the host; an SSH reverse tunnel
// (-R MCPRemotePort:localhost:<dynamic>) bridges the two.
const MCPRemotePort = 19090

// CodePuller pulls committed code from a sandbox into a local git repo.
type CodePuller interface {
	Pull(ctx context.Context, name, remotePath, localRepoDir string) error
}


// OpenPRInput is the input schema for the open_pr tool.
type OpenPRInput struct {
	Path   string `json:"path" jsonschema_description:"Absolute path to the git repo in the sandbox"`
	Repo   string `json:"repo" jsonschema_description:"Target repository as owner/repo (e.g. osbuild/osbuild)"`
	Branch string `json:"branch" jsonschema_description:"Name of the remote branch to push to"`
	Base   string `json:"base,omitempty" jsonschema_description:"Base branch for the PR (default: main)"`
	Title  string `json:"title" jsonschema_description:"PR title"`
	Body   string `json:"body" jsonschema_description:"PR body/description"`
}

// UpdatePRInput is the input schema for the update_pr tool.
type UpdatePRInput struct {
	Path   string `json:"path" jsonschema_description:"Absolute path to the git repo in the sandbox"`
	Repo   string `json:"repo" jsonschema_description:"Target repository as owner/repo (e.g. osbuild/osbuild)"`
	Branch string `json:"branch" jsonschema_description:"Name of the remote branch to push to (must match the existing PR branch)"`
}

// CommentOnPRInput is the input schema for the comment_on_pr tool.
type CommentOnPRInput struct {
	PRURL string `json:"pr_url" jsonschema_description:"URL of the pull request to comment on (must be a PR opened by this task)"`
	Body  string `json:"body" jsonschema_description:"Comment body (markdown supported)"`
	Title string `json:"title,omitempty" jsonschema_description:"Optional new title for the pull request. If empty, the title is not changed."`
}

// PostReviewInput is the input schema for the post_review tool.
type PostReviewInput struct {
	Repo  string `json:"repo" jsonschema_description:"Target repository as owner/repo (e.g. osbuild/osbuild)"`
	PR    int    `json:"pr" jsonschema_description:"Pull request number"`
	Event string `json:"event" jsonschema_description:"Review action: APPROVE, REQUEST_CHANGES, or COMMENT"`
	Body  string `json:"body" jsonschema_description:"Review body text (markdown supported)"`
}

// Server wraps an MCP server that exposes tools for sandbox operations.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
}

func isRepoAllowed(repo string, allowedRepos []string) bool {
	for _, pattern := range allowedRepos {
		if matched, _ := path.Match(pattern, repo); matched {
			return true
		}
	}
	return false
}

// baseBranchForPR looks up the base branch for a previously opened PR
// matching the given repo and branch. Falls back to "main" if not found.
func baseBranchForPR(taskDir *task.Dir, repo, branch string) string {
	state, err := taskDir.LoadState()
	if err != nil {
		return "main"
	}
	for _, pr := range state.Resources.PRs() {
		if pr.Repo == repo && pr.Branch == branch {
			return pr.Base
		}
	}
	return "main"
}

func resolvePushTarget(ctx context.Context, repo string, vcsProvider vcs.Provider) (pushTarget, forkOwner string, err error) {
	forkOwner, err = vcsProvider.AuthenticatedUser(ctx)
	if err != nil {
		return "", "", fmt.Errorf("getting authenticated user: %w", err)
	}

	repoOwner, _, _ := strings.Cut(repo, "/")
	pushTarget = repo
	if forkOwner != repoOwner {
		forkFullName, err := vcsProvider.EnsureFork(ctx, repo)
		if err != nil {
			return "", "", fmt.Errorf("ensuring fork: %w", err)
		}
		pushTarget = forkFullName
	}
	return pushTarget, forkOwner, nil
}

// resolveOriginatingIssue returns the tasks-repo and issue number for a task
// spawned from a GitHub issue (state.Source first, then task name parsing).
func resolveOriginatingIssue(tasksRepo, taskName string, state *task.State) (repo string, issueNum int, ok bool) {
	if state != nil && state.Source != nil && state.Source.IssueNumber > 0 {
		repo = state.Source.TasksRepo
		if repo == "" {
			repo = tasksRepo
		}
		if repo == "" {
			return "", 0, false
		}
		return repo, state.Source.IssueNumber, true
	}
	if tasksRepo == "" {
		return "", 0, false
	}
	n, parsed := tasksource.IssueNumberFromTaskName(tasksRepo, taskName)
	if !parsed {
		return "", 0, false
	}
	return tasksRepo, n, true
}

// New creates a new MCP server. If vcsProvider is non-nil and allowedRepos is
// non-empty, the open_pr tool is registered. When author is non-empty, a
// Co-authored-by trailer is appended to each new commit before pushing.
// tasksRepo is the daemon tasks_repo used to link PRs back to originating issues.
func New(logger *slog.Logger, taskName string, taskDir *task.Dir, puller CodePuller, vcsProvider vcs.Provider, allowedRepos []string, author, tasksRepo string) *Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "orchestrator",
		Version: "0.1.0",
	}, nil)

	if vcsProvider != nil && len(allowedRepos) > 0 {
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name:        "open_pr",
			Description: "Push committed code from the sandbox and open a draft pull request on GitHub. The tool returns the URL of the created PR.",
		}, func(ctx context.Context, req *mcp.CallToolRequest, input *OpenPRInput) (*mcp.CallToolResult, any, error) {
			logger.Info("PR open requested", "task", taskName, "repo", input.Repo)

			if !isRepoAllowed(input.Repo, allowedRepos) {
				logger.Warn("PR open denied: repo not allowed", "task", taskName, "repo", input.Repo)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("repo %q is not in the allowed repos list", input.Repo)},
					},
					IsError: true,
				}, nil, nil
			}

			if err := puller.Pull(ctx, taskName, input.Path, taskDir.RepoPath()); err != nil {
				logger.Error("Code pull failed", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			if input.Base == "" {
				input.Base = "main"
			}

			sourceRef := "gjoll-" + taskName

			if author != "" {
				trailer := fmt.Sprintf("Co-authored-by: %s", author)
				if err := vcsProvider.AddCoAuthorTrailers(ctx, taskDir.RepoPath(), input.Repo, input.Base, sourceRef, trailer); err != nil {
					logger.Error("Failed to add co-author trailers", "task", taskName, "error", err)
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
						},
						IsError: true,
					}, nil, nil
				}
			}

			pushTarget, forkOwner, err := resolvePushTarget(ctx, input.Repo, vcsProvider)
			if err != nil {
				logger.Error("Failed to resolve push target", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			if err := vcsProvider.PushBranch(ctx, taskDir.RepoPath(), pushTarget, input.Branch, sourceRef); err != nil {
				logger.Error("Failed to push branch", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			prURL, err := vcsProvider.CreatePR(ctx, input.Repo, forkOwner, input.Branch, input.Base, input.Title, input.Body)
			if err != nil {
				logger.Error("Failed to create PR", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			prNumber, _ := vcsProvider.PRNumberFromURL(prURL)
			if err := taskDir.AddPR(task.PR{
				URL:    prURL,
				Repo:   input.Repo,
				Branch: input.Branch,
				Base:   input.Base,
				Number: prNumber,
			}); err != nil {
				logger.Warn("Failed to record PR in task state", "task", taskName, "error", err)
			}

			if tasksRepo != "" {
				state, err := taskDir.LoadState()
				if err != nil {
					logger.Warn("Failed to load task state for issue link", "task", taskName, "error", err)
				} else if issueRepo, issueNum, ok := resolveOriginatingIssue(tasksRepo, taskName, state); ok {
					commentBody := fmt.Sprintf("Opened draft pull request for task `%s`:\n\n%s", taskName, prURL)
					if err := vcsProvider.CommentOnIssue(ctx, issueRepo, issueNum, commentBody); err != nil {
						logger.Warn("Failed to comment on originating issue", "task", taskName, "issue", issueNum, "error", err)
					}
				}
			}

			logger.Info("PR created", "task", taskName, "url", prURL, "repo", input.Repo)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: prURL},
				},
			}, nil, nil
		})

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name:        "update_pr",
			Description: "Push committed code from the sandbox to an existing PR",
		}, func(ctx context.Context, req *mcp.CallToolRequest, input *UpdatePRInput) (*mcp.CallToolResult, any, error) {
			logger.Info("PR update requested", "task", taskName, "repo", input.Repo, "branch", input.Branch)

			if !isRepoAllowed(input.Repo, allowedRepos) {
				logger.Warn("PR update denied: repo not allowed", "task", taskName, "repo", input.Repo)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("repo %q is not in the allowed repos list", input.Repo)},
					},
					IsError: true,
				}, nil, nil
			}

			if err := puller.Pull(ctx, taskName, input.Path, taskDir.RepoPath()); err != nil {
				logger.Error("Code pull failed", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("update_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			sourceRef := "gjoll-" + taskName

			if author != "" {
				base := baseBranchForPR(taskDir, input.Repo, input.Branch)
				trailer := fmt.Sprintf("Co-authored-by: %s", author)
				if err := vcsProvider.AddCoAuthorTrailers(ctx, taskDir.RepoPath(), input.Repo, base, sourceRef, trailer); err != nil {
					logger.Error("Failed to add co-author trailers", "task", taskName, "error", err)
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: fmt.Sprintf("update_pr failed: %v", err)},
						},
						IsError: true,
					}, nil, nil
				}
			}

			pushTarget, _, err := resolvePushTarget(ctx, input.Repo, vcsProvider)
			if err != nil {
				logger.Error("Failed to resolve push target", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("update_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			if err := vcsProvider.PushBranch(ctx, taskDir.RepoPath(), pushTarget, input.Branch, sourceRef); err != nil {
				logger.Error("Failed to push branch", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("update_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			logger.Info("Branch updated", "task", taskName, "branch", input.Branch, "target", pushTarget)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Branch %s updated on %s. Use `comment_on_pr` to post a comment about the changes.", input.Branch, pushTarget)},
				},
			}, nil, nil
		})

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name:        "comment_on_pr",
			Description: "Post a comment on a pull request opened by this task",
		}, func(ctx context.Context, req *mcp.CallToolRequest, input *CommentOnPRInput) (*mcp.CallToolResult, any, error) {
			logger.Info("PR comment requested", "task", taskName, "pr_url", input.PRURL)

			state, err := taskDir.LoadState()
			if err != nil {
				logger.Error("Failed to load task state", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("comment_on_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			found := false
			for _, pr := range state.Resources.PRs() {
				if pr.URL == input.PRURL {
					found = true
					break
				}
			}
			if !found {
				logger.Warn("PR comment denied: PR not owned by task", "task", taskName, "pr_url", input.PRURL)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("PR %q was not opened by this task", input.PRURL)},
					},
					IsError: true,
				}, nil, nil
			}

			if err := vcsProvider.CommentOnPR(ctx, input.PRURL, input.Body); err != nil {
				logger.Error("Failed to comment on PR", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("comment_on_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			if input.Title != "" {
				if err := vcsProvider.UpdatePRTitle(ctx, input.PRURL, input.Title); err != nil {
					logger.Error("Failed to update PR title", "task", taskName, "error", err)
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: fmt.Sprintf("comment posted but title update failed: %v", err)},
						},
						IsError: true,
					}, nil, nil
				}
			}

			logger.Info("PR comment posted", "task", taskName, "pr_url", input.PRURL)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Comment posted on %s", input.PRURL)},
				},
			}, nil, nil
		})

		mcp.AddTool(mcpServer, &mcp.Tool{
			Name:        "post_review",
			Description: "Submit a review on a GitHub pull request",
		}, func(ctx context.Context, req *mcp.CallToolRequest, input *PostReviewInput) (*mcp.CallToolResult, any, error) {
			logger.Info("PR review requested", "task", taskName, "repo", input.Repo, "pr", input.PR)

			if !isRepoAllowed(input.Repo, allowedRepos) {
				logger.Warn("PR review denied: repo not allowed", "task", taskName, "repo", input.Repo)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("repo %q is not in the allowed repos list", input.Repo)},
					},
					IsError: true,
				}, nil, nil
			}

			if err := vcsProvider.PostReview(ctx, input.Repo, input.PR, input.Event, input.Body); err != nil {
				logger.Error("Failed to post review", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("post_review failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			logger.Info("PR review posted", "task", taskName, "repo", input.Repo, "pr", input.PR, "event", input.Event)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Review posted on %s#%d (%s)", input.Repo, input.PR, input.Event)},
				},
			}, nil, nil
		})
	}

	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	return &Server{
		httpServer: &http.Server{
			Handler: handler,
		},
	}
}

// Start starts the MCP server on a dynamically allocated port.
// Use Port() to retrieve the assigned port.
func (s *Server) Start() error {
	return s.StartOn("127.0.0.1:0")
}

// Port returns the port the server is listening on, or 0 if not started.
func (s *Server) Port() int {
	if s.listener != nil {
		return s.listener.Addr().(*net.TCPAddr).Port
	}
	return 0
}

// StartOn starts the MCP server on the given address.
func (s *Server) StartOn(addr string) error {
	var err error
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			slog.Error("MCP server error", "error", err)
		}
	}()

	return nil
}

// Addr returns the listener's address (useful when started on :0).
func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// Stop gracefully shuts down the MCP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
