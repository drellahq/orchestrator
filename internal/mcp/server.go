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
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPPort is the default port for the MCP server.
const MCPPort = 19090

// CodePuller pulls committed code from a sandbox into a local git repo.
type CodePuller interface {
	Pull(ctx context.Context, name, remotePath, localRepoDir string) error
}

// PROpener handles GitHub operations for opening pull requests.
type PROpener interface {
	AuthenticatedUser(ctx context.Context) (string, error)
	EnsureFork(ctx context.Context, upstream string) (string, error)
	PushBranch(ctx context.Context, repoDir, forkFullName, branch, sourceRef string) error
	CreatePR(ctx context.Context, upstream, forkOwner, branch, base, title, body string) (string, error)
}

// PullCodeInput is the input schema for the pull_code tool.
type PullCodeInput struct {
	Path string `json:"path" jsonschema_description:"Absolute path to the git repo in the sandbox"`
}

// OpenPRInput is the input schema for the open_pr tool.
type OpenPRInput struct {
	Repo   string `json:"repo" jsonschema_description:"Target repository as owner/repo (e.g. osbuild/osbuild)"`
	Branch string `json:"branch" jsonschema_description:"Branch name to push"`
	Base   string `json:"base,omitempty" jsonschema_description:"Base branch for the PR (default: main)"`
	Title  string `json:"title" jsonschema_description:"PR title"`
	Body   string `json:"body" jsonschema_description:"PR body/description"`
}

// Server wraps an MCP server that exposes the pull_code tool.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
}

// New creates a new MCP server. The pull_code tool calls puller.Pull to fetch
// committed code from the sandbox into the local task directory. If prOpener
// is non-nil and allowedRepos is non-empty, the open_pr tool is registered.
func New(logger *slog.Logger, taskName string, taskDir *task.Dir, puller CodePuller, prOpener PROpener, allowedRepos []string) *Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "orchestrator",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "pull_code",
		Description: "Pull committed code from the sandbox git repo to the host",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input *PullCodeInput) (*mcp.CallToolResult, any, error) {
		logger.Info("Code pull requested", "task", taskName, "path", input.Path)

		if err := puller.Pull(ctx, taskName, input.Path, taskDir.RepoPath()); err != nil {
			logger.Error("Code pull failed", "task", taskName, "error", err)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("pull_code failed: %v", err)},
				},
				IsError: true,
			}, nil, nil
		}

		logger.Info("Code pulled", "task", taskName)
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Code pulled successfully to host"},
			},
		}, nil, nil
	})

	if prOpener != nil && len(allowedRepos) > 0 {
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name:        "open_pr",
			Description: "Open a pull request on GitHub from the pulled code",
		}, func(ctx context.Context, req *mcp.CallToolRequest, input *OpenPRInput) (*mcp.CallToolResult, any, error) {
			logger.Info("PR open requested", "task", taskName, "repo", input.Repo)

			// Validate repo against allowlist
			allowed := false
			for _, pattern := range allowedRepos {
				if matched, _ := path.Match(pattern, input.Repo); matched {
					allowed = true
					break
				}
			}
			if !allowed {
				logger.Warn("PR open denied: repo not allowed", "task", taskName, "repo", input.Repo)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("repo %q is not in the allowed repos list", input.Repo)},
					},
					IsError: true,
				}, nil, nil
			}

			if input.Base == "" {
				input.Base = "main"
			}

			forkOwner, err := prOpener.AuthenticatedUser(ctx)
			if err != nil {
				logger.Error("Failed to get authenticated user", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			// If the authenticated user owns the upstream repo, push
			// directly instead of forking (you can't fork your own repo).
			repoOwner, _, _ := strings.Cut(input.Repo, "/")
			pushTarget := input.Repo
			if forkOwner != repoOwner {
				forkFullName, err := prOpener.EnsureFork(ctx, input.Repo)
				if err != nil {
					logger.Error("Failed to ensure fork", "task", taskName, "error", err)
					return &mcp.CallToolResult{
						Content: []mcp.Content{
							&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
						},
						IsError: true,
					}, nil, nil
				}
				pushTarget = forkFullName
			}

			sourceRef := "gjoll/" + taskName
			if err := prOpener.PushBranch(ctx, taskDir.RepoPath(), pushTarget, input.Branch, sourceRef); err != nil {
				logger.Error("Failed to push branch", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			prURL, err := prOpener.CreatePR(ctx, input.Repo, forkOwner, input.Branch, input.Base, input.Title, input.Body)
			if err != nil {
				logger.Error("Failed to create PR", "task", taskName, "error", err)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{Text: fmt.Sprintf("open_pr failed: %v", err)},
					},
					IsError: true,
				}, nil, nil
			}

			logger.Info("PR created", "task", taskName, "url", prURL, "repo", input.Repo)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: prURL},
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

// Start starts the MCP server on 127.0.0.1:MCPPort.
func (s *Server) Start() error {
	return s.StartOn(fmt.Sprintf("127.0.0.1:%d", MCPPort))
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
