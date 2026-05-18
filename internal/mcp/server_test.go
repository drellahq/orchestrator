package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/drellabot/orchestrator/internal/task"
	"github.com/drellabot/orchestrator/internal/vcs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubPuller implements CodePuller for testing.
type stubPuller struct {
	err      error
	called   bool
	gotName  string
	gotPath  string
	gotLocal string
}

func (s *stubPuller) Pull(_ context.Context, name, remotePath, localRepoDir string) error {
	s.called = true
	s.gotName = name
	s.gotPath = remotePath
	s.gotLocal = localRepoDir
	return s.err
}

// stubVCSProvider implements vcs.Provider for testing.
type stubVCSProvider struct {
	user    string
	userErr error

	forkName   string
	forkErr    error
	forkCalled bool

	pushErr     error
	gotRepoDir  string
	gotForkName string
	gotBranch   string
	gotSource   string

	prURL     string
	prErr     error
	gotPRRepo string
	gotPRHead string
	gotPRBase string

	trailerErr    error
	trailerCalled bool
	gotTrailer    string
	gotTrailerBase string

	commentErr    error
	commentCalled bool
	gotCommentURL string
	gotCommentBody string

	issueCommentErr     error
	issueCommentCalled  bool
	gotIssueCommentRepo string
	gotIssueCommentNum  int
	gotIssueCommentBody string

	titleErr       error
	titleCalled    bool
	gotTitleURL    string
	gotTitleTitle   string

	reviewErr    error
	reviewCalled bool
	gotReviewRepo  string
	gotReviewPR    int
	gotReviewEvent string
	gotReviewBody  string
}

func (s *stubVCSProvider) AuthenticatedUser(_ context.Context) (string, error) {
	return s.user, s.userErr
}

func (s *stubVCSProvider) EnsureFork(_ context.Context, upstream string) (string, error) {
	s.forkCalled = true
	return s.forkName, s.forkErr
}

func (s *stubVCSProvider) PushBranch(_ context.Context, repoDir, forkFullName, branch, sourceRef string) error {
	s.gotRepoDir = repoDir
	s.gotForkName = forkFullName
	s.gotBranch = branch
	s.gotSource = sourceRef
	return s.pushErr
}

func (s *stubVCSProvider) CreatePR(_ context.Context, upstream, forkOwner, branch, base, title, body string) (string, error) {
	s.gotPRRepo = upstream
	s.gotPRHead = forkOwner + ":" + branch
	s.gotPRBase = base
	return s.prURL, s.prErr
}

func (s *stubVCSProvider) AddCoAuthorTrailers(_ context.Context, repoDir, upstream, base, sourceRef, trailer string) error {
	s.trailerCalled = true
	s.gotTrailer = trailer
	s.gotTrailerBase = base
	return s.trailerErr
}

func (s *stubVCSProvider) CommentOnPR(_ context.Context, prURL, body string) error {
	s.commentCalled = true
	s.gotCommentURL = prURL
	s.gotCommentBody = body
	return s.commentErr
}

func (s *stubVCSProvider) CommentOnIssue(_ context.Context, repo string, issue int, body string) error {
	s.issueCommentCalled = true
	s.gotIssueCommentRepo = repo
	s.gotIssueCommentNum = issue
	s.gotIssueCommentBody = body
	return s.issueCommentErr
}

func (s *stubVCSProvider) UpdatePRTitle(_ context.Context, prURL, title string) error {
	s.titleCalled = true
	s.gotTitleURL = prURL
	s.gotTitleTitle = title
	return s.titleErr
}

func (s *stubVCSProvider) PostReview(_ context.Context, repo string, pr int, event, body string) error {
	s.reviewCalled = true
	s.gotReviewRepo = repo
	s.gotReviewPR = pr
	s.gotReviewEvent = event
	s.gotReviewBody = body
	return s.reviewErr
}

func (s *stubVCSProvider) IsPROpen(_ context.Context, repo string, prNumber int) (bool, error) {
	return true, nil
}

func (s *stubVCSProvider) FetchAllComments(_ context.Context, repo string, prNumber int) ([]vcs.Comment, error) {
	return nil, nil
}

func (s *stubVCSProvider) ReactToComment(_ context.Context, repo string, commentID int64, commentType vcs.CommentType, reaction string) error {
	return nil
}

func (s *stubVCSProvider) ReactToIssue(_ context.Context, repo string, issueNumber int, reaction string) error {
	return nil
}

func (s *stubVCSProvider) ListRepoFiles(_ context.Context, repo, branch, dir string) ([]string, error) {
	return nil, nil
}

func (s *stubVCSProvider) GetFileContent(_ context.Context, repo, branch, path string) (string, error) {
	return "", nil
}

func (s *stubVCSProvider) ListIssues(_ context.Context, repo string) ([]vcs.Issue, error) {
	return nil, nil
}

func (s *stubVCSProvider) CloneRepo(_ context.Context, repo, dir string) error {
	return nil
}

func (s *stubVCSProvider) PRNumberFromURL(url string) (int, error) {
	const prefix = "/pull/"
	idx := strings.LastIndex(url, prefix)
	if idx == -1 {
		return 0, fmt.Errorf("no /pull/ in URL")
	}
	numStr := url[idx+len(prefix):]
	if i := strings.Index(numStr, "/"); i != -1 {
		numStr = numStr[:i]
	}
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (s *stubVCSProvider) RepoURL(repo string) string {
	return "https://github.com/" + repo + ".git"
}

func startTestServer(t *testing.T, puller CodePuller, vcsProvider vcs.Provider, allowedRepos []string, authors ...string) (*task.Dir, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	td, err := task.Create(dir, "test-task")
	if err != nil {
		t.Fatal(err)
	}

	var author string
	if len(authors) > 0 {
		author = authors[0]
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := New(logger, "test-task", td, puller, vcsProvider, allowedRepos, author, "")
	if err := s.StartOn("127.0.0.1:0"); err != nil {
		t.Fatalf("StartOn() error: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Stop(context.Background()); err != nil {
			t.Errorf("Stop() error: %v", err)
		}
	})

	endpoint := fmt.Sprintf("http://%s", s.Addr().String())
	return td, s, endpoint
}

func connectClient(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "0.1.0",
	}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: endpoint,
	}, nil)
	if err != nil {
		t.Fatalf("client connect error: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestStartAllocatesDynamicPort(t *testing.T) {
	dir := t.TempDir()
	td, err := task.Create(dir, "dyn-port-test")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	s1 := New(logger, "task-1", td, nil, nil, nil, "", "")
	if err := s1.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer s1.Stop(context.Background())

	if s1.Port() == 0 {
		t.Fatal("Port() returned 0 after Start()")
	}

	dir2 := t.TempDir()
	td2, _ := task.Create(dir2, "dyn-port-test-2")
	s2 := New(logger, "task-2", td2, nil, nil, nil, "", "")
	if err := s2.Start(); err != nil {
		t.Fatalf("Start() second server: %v", err)
	}
	defer s2.Stop(context.Background())

	if s1.Port() == s2.Port() {
		t.Errorf("two servers got the same port %d", s1.Port())
	}
}

func TestServerListTools(t *testing.T) {
	opener := &stubVCSProvider{user: "testuser", forkName: "testuser/repo", prURL: "https://github.com/org/repo/pull/1"}
	_, _, endpoint := startTestServer(t, &stubPuller{}, opener, []string{"org/*"})
	session := connectClient(t, endpoint)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}

	wantTools := map[string]bool{"open_pr": false, "update_pr": false, "comment_on_pr": false, "post_review": false}
	for _, tool := range result.Tools {
		if _, ok := wantTools[tool.Name]; ok {
			wantTools[tool.Name] = true
		}
	}
	for name, found := range wantTools {
		if !found {
			t.Errorf("%s tool not found in tool list", name)
		}
	}
}

func TestOpenPRTool(t *testing.T) {
	tests := []struct {
		name           string
		puller         *stubPuller
		opener         *stubVCSProvider
		allowedRepos   []string
		input          map[string]any
		wantError      bool
		wantText       string
		wantForkCalled bool
		wantPullCalled bool
	}{
		{
			name:   "successful PR via fork",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
				prURL:    "https://github.com/osbuild/osbuild/pull/42",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"base":   "main",
				"title":  "Fix bug",
				"body":   "This fixes the bug",
			},
			wantText:       "https://github.com/osbuild/osbuild/pull/42",
			wantForkCalled: true,
			wantPullCalled: true,
		},
		{
			name:   "user owns repo skips fork",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:  "osbuild",
				prURL: "https://github.com/osbuild/osbuild/pull/99",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"base":   "main",
				"title":  "Fix bug",
				"body":   "Direct push",
			},
			wantText:       "https://github.com/osbuild/osbuild/pull/99",
			wantForkCalled: false,
			wantPullCalled: true,
		},
		{
			name:   "default base branch",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
				prURL:    "https://github.com/osbuild/osbuild/pull/43",
			},
			allowedRepos:   []string{"osbuild/osbuild"},
			wantForkCalled: true,
			wantPullCalled: true,
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "This fixes the bug",
			},
			wantText: "https://github.com/osbuild/osbuild/pull/43",
		},
		{
			name:         "repo not allowed",
			puller:       &stubPuller{},
			opener:       &stubVCSProvider{},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "evil/repo",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "This fixes the bug",
			},
			wantError:      true,
			wantText:       "not in the allowed repos list",
			wantPullCalled: false,
		},
		{
			name:   "pull failure",
			puller: &stubPuller{err: fmt.Errorf("connection refused")},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError:      true,
			wantText:       "connection refused",
			wantPullCalled: true,
		},
		{
			name:   "fork failure",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:    "testuser",
				forkErr: fmt.Errorf("fork failed"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantForkCalled: true,
			wantPullCalled: true,
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError: true,
			wantText:  "fork failed",
		},
		{
			name:   "push failure",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
				pushErr:  fmt.Errorf("push rejected"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantForkCalled: true,
			wantPullCalled: true,
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError: true,
			wantText:  "push rejected",
		},
		{
			name:   "PR creation failure",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
				prErr:    fmt.Errorf("duplicate PR"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantForkCalled: true,
			wantPullCalled: true,
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError: true,
			wantText:  "duplicate PR",
		},
		{
			name:   "auth failure",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				userErr: fmt.Errorf("not authenticated"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantPullCalled: true,
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError: true,
			wantText:  "not authenticated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			td, _, endpoint := startTestServer(t, tt.puller, tt.opener, tt.allowedRepos)
			session := connectClient(t, endpoint)

			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "open_pr",
				Arguments: tt.input,
			})
			if err != nil {
				t.Fatalf("CallTool() protocol error: %v", err)
			}

			if result.IsError != tt.wantError {
				t.Errorf("IsError = %v, want %v", result.IsError, tt.wantError)
			}

			var text string
			for _, c := range result.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text = tc.Text
				}
			}
			if !strings.Contains(text, tt.wantText) {
				t.Errorf("result text %q does not contain %q", text, tt.wantText)
			}

			if tt.puller.called != tt.wantPullCalled {
				t.Errorf("puller.called = %v, want %v", tt.puller.called, tt.wantPullCalled)
			}
			if tt.wantPullCalled && tt.puller.gotPath != "/test/project" {
				t.Errorf("puller.gotPath = %q, want %q", tt.puller.gotPath, "/test/project")
			}

			// Verify default base branch
			if tt.name == "default base branch" && tt.opener.gotPRBase != "main" {
				t.Errorf("base = %q, want %q", tt.opener.gotPRBase, "main")
			}

			// Verify sourceRef includes task name
			if !tt.wantError && tt.opener.gotSource != "gjoll-test-task" {
				t.Errorf("sourceRef = %q, want %q", tt.opener.gotSource, "gjoll-test-task")
			}

			if tt.opener.forkCalled != tt.wantForkCalled {
				t.Errorf("forkCalled = %v, want %v", tt.opener.forkCalled, tt.wantForkCalled)
			}

			// When user owns repo, push target should be the upstream itself
			if tt.name == "user owns repo skips fork" && tt.opener.gotForkName != "osbuild/osbuild" {
				t.Errorf("pushTarget = %q, want %q", tt.opener.gotForkName, "osbuild/osbuild")
			}

			// Verify PR was recorded in task state on success
			state, stateErr := td.LoadState()
			if stateErr != nil {
				t.Fatalf("LoadState() error: %v", stateErr)
			}
			if tt.wantError {
				if len(state.Resources.PRs()) != 0 {
					t.Errorf("expected no PRs on error, got %d", len(state.Resources.PRs()))
				}
			} else {
				if len(state.Resources.PRs()) != 1 {
					t.Fatalf("expected 1 PR, got %d", len(state.Resources.PRs()))
				}
				pr := state.Resources.PRs()[0]
				if pr.URL != tt.wantText {
					t.Errorf("PR URL = %q, want %q", pr.URL, tt.wantText)
				}
				if pr.Repo != tt.input["repo"] {
					t.Errorf("PR Repo = %q, want %q", pr.Repo, tt.input["repo"])
				}
				if pr.Branch != tt.input["branch"] {
					t.Errorf("PR Branch = %q, want %q", pr.Branch, tt.input["branch"])
				}
			}
		})
	}
}

func TestOpenPRToolWithAuthor(t *testing.T) {
	t.Run("calls AddCoAuthorTrailers when author is set", func(t *testing.T) {
		puller := &stubPuller{}
		opener := &stubVCSProvider{
			user:     "testuser",
			forkName: "testuser/osbuild",
			prURL:    "https://github.com/osbuild/osbuild/pull/42",
		}
		_, _, endpoint := startTestServer(t, puller, opener, []string{"osbuild/*"}, "Jane Doe <jane@example.com>")
		session := connectClient(t, endpoint)

		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "open_pr",
			Arguments: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"base":   "main",
				"title":  "Fix bug",
				"body":   "body",
			},
		})
		if err != nil {
			t.Fatalf("CallTool() error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result")
		}

		if !opener.trailerCalled {
			t.Error("AddCoAuthorTrailers was not called")
		}
		if opener.gotTrailer != "Co-authored-by: Jane Doe <jane@example.com>" {
			t.Errorf("trailer = %q, want %q", opener.gotTrailer, "Co-authored-by: Jane Doe <jane@example.com>")
		}
		if opener.gotTrailerBase != "main" {
			t.Errorf("trailer base = %q, want %q", opener.gotTrailerBase, "main")
		}
	})

	t.Run("skips AddCoAuthorTrailers when author is empty", func(t *testing.T) {
		puller := &stubPuller{}
		opener := &stubVCSProvider{
			user:     "testuser",
			forkName: "testuser/osbuild",
			prURL:    "https://github.com/osbuild/osbuild/pull/42",
		}
		_, _, endpoint := startTestServer(t, puller, opener, []string{"osbuild/*"})
		session := connectClient(t, endpoint)

		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "open_pr",
			Arguments: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"base":   "main",
				"title":  "Fix bug",
				"body":   "body",
			},
		})
		if err != nil {
			t.Fatalf("CallTool() error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result")
		}

		if opener.trailerCalled {
			t.Error("AddCoAuthorTrailers should not be called when author is empty")
		}
	})

	t.Run("returns error when AddCoAuthorTrailers fails", func(t *testing.T) {
		puller := &stubPuller{}
		opener := &stubVCSProvider{
			user:       "testuser",
			forkName:   "testuser/osbuild",
			prURL:      "https://github.com/osbuild/osbuild/pull/42",
			trailerErr: fmt.Errorf("filter-branch failed"),
		}
		_, _, endpoint := startTestServer(t, puller, opener, []string{"osbuild/*"}, "Jane Doe <jane@example.com>")
		session := connectClient(t, endpoint)

		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "open_pr",
			Arguments: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"base":   "main",
				"title":  "Fix bug",
				"body":   "body",
			},
		})
		if err != nil {
			t.Fatalf("CallTool() error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result")
		}
		var text string
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text = tc.Text
			}
		}
		if !strings.Contains(text, "filter-branch failed") {
			t.Errorf("error text %q does not mention filter-branch", text)
		}
	})
}

func TestResolveOriginatingIssue(t *testing.T) {
	state := &task.State{
		Source: &task.Source{TasksRepo: "org/tasks", IssueNumber: 42},
	}
	repo, num, ok := resolveOriginatingIssue("org/tasks", "tasks-42-add_dark_mode", state)
	if !ok || repo != "org/tasks" || num != 42 {
		t.Errorf("got (%q, %d, %v), want (org/tasks, 42, true)", repo, num, ok)
	}

	repo, num, ok = resolveOriginatingIssue("org/tasks", "tasks-42-add_dark_mode", &task.State{})
	if !ok || num != 42 {
		t.Errorf("fallback parse: got (%q, %d, %v), want (org/tasks, 42, true)", repo, num, ok)
	}

	_, _, ok = resolveOriginatingIssue("", "tasks-42-add_dark_mode", &task.State{})
	if ok {
		t.Error("expected false when tasksRepo is empty and no state source")
	}
}

func TestOpenPRLinksOriginatingIssue(t *testing.T) {
	puller := &stubPuller{}
	opener := &stubPROpener{
		user:     "testuser",
		forkName: "testuser/org",
		prURL:    "https://github.com/org/repo/pull/99",
	}

	dir := t.TempDir()
	td, err := task.Create(dir, "tasks-42-add_dark_mode")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveSource("org/tasks", 42); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := New(logger, "tasks-42-add_dark_mode", td, puller, opener, []string{"org/*"}, "", "org/tasks")
	if err := s.StartOn("127.0.0.1:0"); err != nil {
		t.Fatalf("StartOn() error: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Stop(context.Background()); err != nil {
			t.Errorf("Stop() error: %v", err)
		}
	})

	endpoint := fmt.Sprintf("http://%s", s.Addr().String())
	session := connectClient(t, endpoint)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "open_pr",
		Arguments: map[string]any{
			"path":   "/test/project",
			"repo":   "org/repo",
			"branch": "fix-bug",
			"title":  "Fix bug",
			"body":   "body",
		},
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result")
	}

	if !opener.issueCommentCalled {
		t.Fatal("CommentOnIssue was not called")
	}
	if opener.gotIssueCommentRepo != "org/tasks" {
		t.Errorf("issue repo = %q, want org/tasks", opener.gotIssueCommentRepo)
	}
	if opener.gotIssueCommentNum != 42 {
		t.Errorf("issue number = %d, want 42", opener.gotIssueCommentNum)
	}
	wantBody := "Opened draft pull request for task `tasks-42-add_dark_mode`:\n\nhttps://github.com/org/repo/pull/99"
	if opener.gotIssueCommentBody != wantBody {
		t.Errorf("comment body = %q, want %q", opener.gotIssueCommentBody, wantBody)
	}
}

func TestOpenPRSkipsIssueLinkWithoutSource(t *testing.T) {
	puller := &stubPuller{}
	opener := &stubPROpener{
		user:     "testuser",
		forkName: "testuser/org",
		prURL:    "https://github.com/org/repo/pull/99",
	}

	_, _, endpoint := startTestServer(t, puller, opener, []string{"org/*"})
	session := connectClient(t, endpoint)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "open_pr",
		Arguments: map[string]any{
			"path":   "/test/project",
			"repo":   "org/repo",
			"branch": "fix-bug",
			"title":  "Fix bug",
			"body":   "body",
		},
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result")
	}
	if opener.issueCommentCalled {
		t.Error("CommentOnIssue should not be called without originating issue")
	}
}

func TestUpdatePRToolWithAuthor(t *testing.T) {
	t.Run("looks up base branch from task state", func(t *testing.T) {
		puller := &stubPuller{}
		opener := &stubVCSProvider{
			user:     "testuser",
			forkName: "testuser/osbuild",
		}
		td, _, endpoint := startTestServer(t, puller, opener, []string{"osbuild/*"}, "Jane Doe <jane@example.com>")

		// Record a PR so update_pr can look up the base branch
		if err := td.AddPR(task.PR{
			URL:    "https://github.com/osbuild/osbuild/pull/1",
			Repo:   "osbuild/osbuild",
			Branch: "fix-bug",
			Base:   "develop",
		}); err != nil {
			t.Fatal(err)
		}

		session := connectClient(t, endpoint)

		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "update_pr",
			Arguments: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
			},
		})
		if err != nil {
			t.Fatalf("CallTool() error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result")
		}

		if !opener.trailerCalled {
			t.Error("AddCoAuthorTrailers was not called")
		}
		if opener.gotTrailerBase != "develop" {
			t.Errorf("trailer base = %q, want %q (from task state)", opener.gotTrailerBase, "develop")
		}
	})

	t.Run("defaults to main when no PR recorded", func(t *testing.T) {
		puller := &stubPuller{}
		opener := &stubVCSProvider{
			user:     "testuser",
			forkName: "testuser/osbuild",
		}
		_, _, endpoint := startTestServer(t, puller, opener, []string{"osbuild/*"}, "Jane Doe <jane@example.com>")
		session := connectClient(t, endpoint)

		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "update_pr",
			Arguments: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
			},
		})
		if err != nil {
			t.Fatalf("CallTool() error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result")
		}

		if opener.gotTrailerBase != "main" {
			t.Errorf("trailer base = %q, want %q (default)", opener.gotTrailerBase, "main")
		}
	})
}

func TestBaseBranchForPR(t *testing.T) {
	dir := t.TempDir()
	td, err := task.Create(dir, "test-task")
	if err != nil {
		t.Fatal(err)
	}

	// No PRs recorded — should default to "main"
	if got := baseBranchForPR(td, "org/repo", "my-branch"); got != "main" {
		t.Errorf("baseBranchForPR (no PRs) = %q, want %q", got, "main")
	}

	// Record a PR
	if err := td.AddPR(task.PR{
		URL:    "https://github.com/org/repo/pull/1",
		Repo:   "org/repo",
		Branch: "my-branch",
		Base:   "develop",
	}); err != nil {
		t.Fatal(err)
	}

	if got := baseBranchForPR(td, "org/repo", "my-branch"); got != "develop" {
		t.Errorf("baseBranchForPR (matching PR) = %q, want %q", got, "develop")
	}

	// Non-matching branch — should default
	if got := baseBranchForPR(td, "org/repo", "other-branch"); got != "main" {
		t.Errorf("baseBranchForPR (no match) = %q, want %q", got, "main")
	}
}

func TestUpdatePRTool(t *testing.T) {
	tests := []struct {
		name           string
		puller         *stubPuller
		opener         *stubVCSProvider
		allowedRepos   []string
		input          map[string]any
		wantError      bool
		wantText       string
		wantForkCalled bool
		wantPullCalled bool
	}{
		{
			name:   "successful update via fork",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
			},
			wantText:       "Branch fix-bug updated on testuser/osbuild",
			wantForkCalled: true,
			wantPullCalled: true,
		},
		{
			name:   "user owns repo skips fork",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user: "osbuild",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
			},
			wantText:       "Branch fix-bug updated on osbuild/osbuild",
			wantForkCalled: false,
			wantPullCalled: true,
		},
		{
			name:         "repo not allowed",
			puller:       &stubPuller{},
			opener:       &stubVCSProvider{},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "evil/repo",
				"branch": "fix-bug",
			},
			wantError:      true,
			wantText:       "not in the allowed repos list",
			wantPullCalled: false,
		},
		{
			name:   "pull failure",
			puller: &stubPuller{err: fmt.Errorf("connection refused")},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
			},
			wantError:      true,
			wantText:       "connection refused",
			wantPullCalled: true,
		},
		{
			name:   "push failure",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				user:     "testuser",
				forkName: "testuser/osbuild",
				pushErr:  fmt.Errorf("push rejected"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantForkCalled: true,
			wantPullCalled: true,
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
			},
			wantError: true,
			wantText:  "push rejected",
		},
		{
			name:   "auth failure",
			puller: &stubPuller{},
			opener: &stubVCSProvider{
				userErr: fmt.Errorf("not authenticated"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantPullCalled: true,
			input: map[string]any{
				"path":   "/test/project",
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
			},
			wantError: true,
			wantText:  "not authenticated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, endpoint := startTestServer(t, tt.puller, tt.opener, tt.allowedRepos)
			session := connectClient(t, endpoint)

			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "update_pr",
				Arguments: tt.input,
			})
			if err != nil {
				t.Fatalf("CallTool() protocol error: %v", err)
			}

			if result.IsError != tt.wantError {
				t.Errorf("IsError = %v, want %v", result.IsError, tt.wantError)
			}

			var text string
			for _, c := range result.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text = tc.Text
				}
			}
			if !strings.Contains(text, tt.wantText) {
				t.Errorf("result text %q does not contain %q", text, tt.wantText)
			}

			if tt.puller.called != tt.wantPullCalled {
				t.Errorf("puller.called = %v, want %v", tt.puller.called, tt.wantPullCalled)
			}

			if tt.opener.forkCalled != tt.wantForkCalled {
				t.Errorf("forkCalled = %v, want %v", tt.opener.forkCalled, tt.wantForkCalled)
			}

			// Verify sourceRef includes task name
			if !tt.wantError && tt.opener.gotSource != "gjoll-test-task" {
				t.Errorf("sourceRef = %q, want %q", tt.opener.gotSource, "gjoll-test-task")
			}

			// When user owns repo, push target should be the upstream itself
			if tt.name == "user owns repo skips fork" && tt.opener.gotForkName != "osbuild/osbuild" {
				t.Errorf("pushTarget = %q, want %q", tt.opener.gotForkName, "osbuild/osbuild")
			}
		})
	}
}

func TestCommentOnPRTool(t *testing.T) {
	const ownedPRURL = "https://github.com/osbuild/osbuild/pull/42"

	tests := []struct {
		name              string
		opener            *stubVCSProvider
		seedPR            bool // whether to add a PR to task state before calling
		input             map[string]any
		wantError         bool
		wantText          string
		wantCommentCalled bool
		wantTitleCalled   bool
		wantTitle         string
	}{
		{
			name:   "successful comment on owned PR",
			opener: &stubVCSProvider{user: "testuser"},
			seedPR: true,
			input: map[string]any{
				"pr_url": ownedPRURL,
				"body":   "Pushed new changes: updated tests",
			},
			wantText:          "Comment posted on",
			wantCommentCalled: true,
		},
		{
			name:   "successful comment with title update",
			opener: &stubVCSProvider{user: "testuser"},
			seedPR: true,
			input: map[string]any{
				"pr_url": ownedPRURL,
				"body":   "Updated the PR",
				"title":  "New PR title",
			},
			wantText:          "Comment posted on",
			wantCommentCalled: true,
			wantTitleCalled:   true,
			wantTitle:         "New PR title",
		},
		{
			name:   "title not updated when empty",
			opener: &stubVCSProvider{user: "testuser"},
			seedPR: true,
			input: map[string]any{
				"pr_url": ownedPRURL,
				"body":   "Just a comment",
			},
			wantText:          "Comment posted on",
			wantCommentCalled: true,
			wantTitleCalled:   false,
		},
		{
			name:   "title update failure",
			opener: &stubVCSProvider{user: "testuser", titleErr: fmt.Errorf("title update forbidden")},
			seedPR: true,
			input: map[string]any{
				"pr_url": ownedPRURL,
				"body":   "comment body",
				"title":  "New title",
			},
			wantError:         true,
			wantText:          "title update forbidden",
			wantCommentCalled: true,
			wantTitleCalled:   true,
			wantTitle:         "New title",
		},
		{
			name:   "rejected for unowned PR",
			opener: &stubVCSProvider{user: "testuser"},
			seedPR: false,
			input: map[string]any{
				"pr_url": "https://github.com/other/repo/pull/99",
				"body":   "sneaky comment",
			},
			wantError:         true,
			wantText:          "was not opened by this task",
			wantCommentCalled: false,
		},
		{
			name:   "gh comment failure",
			opener: &stubVCSProvider{user: "testuser", commentErr: fmt.Errorf("forbidden")},
			seedPR: true,
			input: map[string]any{
				"pr_url": ownedPRURL,
				"body":   "test comment",
			},
			wantError:         true,
			wantText:          "forbidden",
			wantCommentCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			td, _, endpoint := startTestServer(t, &stubPuller{}, tt.opener, []string{"osbuild/*"})

			if tt.seedPR {
				if err := td.AddPR(task.PR{
					URL:    ownedPRURL,
					Repo:   "osbuild/osbuild",
					Branch: "fix-bug",
					Base:   "main",
				}); err != nil {
					t.Fatalf("AddPR() error: %v", err)
				}
			}

			session := connectClient(t, endpoint)
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "comment_on_pr",
				Arguments: tt.input,
			})
			if err != nil {
				t.Fatalf("CallTool() protocol error: %v", err)
			}

			if result.IsError != tt.wantError {
				t.Errorf("IsError = %v, want %v", result.IsError, tt.wantError)
			}

			var text string
			for _, c := range result.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text = tc.Text
				}
			}
			if !strings.Contains(text, tt.wantText) {
				t.Errorf("result text %q does not contain %q", text, tt.wantText)
			}

			if tt.opener.commentCalled != tt.wantCommentCalled {
				t.Errorf("commentCalled = %v, want %v", tt.opener.commentCalled, tt.wantCommentCalled)
			}

			if tt.wantCommentCalled && tt.opener.gotCommentURL != tt.input["pr_url"] {
				t.Errorf("gotCommentURL = %q, want %q", tt.opener.gotCommentURL, tt.input["pr_url"])
			}
			if tt.wantCommentCalled && tt.opener.gotCommentBody != tt.input["body"] {
				t.Errorf("gotCommentBody = %q, want %q", tt.opener.gotCommentBody, tt.input["body"])
			}

			if tt.opener.titleCalled != tt.wantTitleCalled {
				t.Errorf("titleCalled = %v, want %v", tt.opener.titleCalled, tt.wantTitleCalled)
			}
			if tt.wantTitleCalled && tt.opener.gotTitleURL != tt.input["pr_url"] {
				t.Errorf("gotTitleURL = %q, want %q", tt.opener.gotTitleURL, tt.input["pr_url"])
			}
			if tt.wantTitleCalled && tt.opener.gotTitleTitle != tt.wantTitle {
				t.Errorf("gotTitleTitle = %q, want %q", tt.opener.gotTitleTitle, tt.wantTitle)
			}
		})
	}
}

func TestPostReviewTool(t *testing.T) {
	tests := []struct {
		name             string
		opener           *stubVCSProvider
		allowedRepos     []string
		input            map[string]any
		wantError        bool
		wantText         string
		wantReviewCalled bool
	}{
		{
			name:         "successful review",
			opener:       &stubVCSProvider{user: "testuser"},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"repo":  "osbuild/osbuild",
				"pr":    42,
				"event": "APPROVE",
				"body":  "LGTM!",
			},
			wantText:         "Review posted on osbuild/osbuild#42 (APPROVE)",
			wantReviewCalled: true,
		},
		{
			name:         "repo not allowed",
			opener:       &stubVCSProvider{user: "testuser"},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"repo":  "evil/repo",
				"pr":    1,
				"event": "APPROVE",
				"body":  "sneaky",
			},
			wantError:        true,
			wantText:         "not in the allowed repos list",
			wantReviewCalled: false,
		},
		{
			name:         "invalid event",
			opener:       &stubVCSProvider{user: "testuser", reviewErr: fmt.Errorf("invalid review event \"INVALID\": must be APPROVE, REQUEST_CHANGES, or COMMENT")},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"repo":  "osbuild/osbuild",
				"pr":    1,
				"event": "INVALID",
				"body":  "bad event",
			},
			wantError:        true,
			wantText:         "invalid review event",
			wantReviewCalled: true,
		},
		{
			name:         "gh command failure",
			opener:       &stubVCSProvider{user: "testuser", reviewErr: fmt.Errorf("gh command failed")},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"repo":  "osbuild/osbuild",
				"pr":    1,
				"event": "COMMENT",
				"body":  "test",
			},
			wantError:        true,
			wantText:         "gh command failed",
			wantReviewCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, endpoint := startTestServer(t, &stubPuller{}, tt.opener, tt.allowedRepos)
			session := connectClient(t, endpoint)

			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "post_review",
				Arguments: tt.input,
			})
			if err != nil {
				t.Fatalf("CallTool() protocol error: %v", err)
			}

			if result.IsError != tt.wantError {
				t.Errorf("IsError = %v, want %v", result.IsError, tt.wantError)
			}

			var text string
			for _, c := range result.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text = tc.Text
				}
			}
			if !strings.Contains(text, tt.wantText) {
				t.Errorf("result text %q does not contain %q", text, tt.wantText)
			}

			if tt.opener.reviewCalled != tt.wantReviewCalled {
				t.Errorf("reviewCalled = %v, want %v", tt.opener.reviewCalled, tt.wantReviewCalled)
			}

			if tt.wantReviewCalled {
				if tt.opener.gotReviewRepo != tt.input["repo"] {
					t.Errorf("gotReviewRepo = %q, want %q", tt.opener.gotReviewRepo, tt.input["repo"])
				}
				if tt.opener.gotReviewBody != tt.input["body"] {
					t.Errorf("gotReviewBody = %q, want %q", tt.opener.gotReviewBody, tt.input["body"])
				}
			}
		})
	}
}
