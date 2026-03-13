package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellabot/orchestrator/internal/task"
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

// stubPROpener implements PROpener for testing.
type stubPROpener struct {
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
}

func (s *stubPROpener) AuthenticatedUser(_ context.Context) (string, error) {
	return s.user, s.userErr
}

func (s *stubPROpener) EnsureFork(_ context.Context, upstream string) (string, error) {
	s.forkCalled = true
	return s.forkName, s.forkErr
}

func (s *stubPROpener) PushBranch(_ context.Context, repoDir, forkFullName, branch, sourceRef string) error {
	s.gotRepoDir = repoDir
	s.gotForkName = forkFullName
	s.gotBranch = branch
	s.gotSource = sourceRef
	return s.pushErr
}

func (s *stubPROpener) CreatePR(_ context.Context, upstream, forkOwner, branch, base, title, body string) (string, error) {
	s.gotPRRepo = upstream
	s.gotPRHead = forkOwner + ":" + branch
	s.gotPRBase = base
	return s.prURL, s.prErr
}

func startTestServer(t *testing.T, puller CodePuller, prOpener PROpener, allowedRepos []string) (*task.Dir, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	td, err := task.Create(dir, "test-task")
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := New(logger, "test-task", td, puller, prOpener, allowedRepos)
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

func TestServerListTools(t *testing.T) {
	opener := &stubPROpener{user: "testuser", forkName: "testuser/repo", prURL: "https://github.com/org/repo/pull/1"}
	_, _, endpoint := startTestServer(t, &stubPuller{}, opener, []string{"org/*"})
	session := connectClient(t, endpoint)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}

	wantTools := map[string]bool{"pull_code": false, "open_pr": false}
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

func TestPullCodeTool(t *testing.T) {
	tests := []struct {
		name      string
		puller    *stubPuller
		wantError bool
		wantText  string
	}{
		{
			name:     "successful pull",
			puller:   &stubPuller{},
			wantText: "Code pulled successfully",
		},
		{
			name:      "failed pull",
			puller:    &stubPuller{err: fmt.Errorf("connection refused")},
			wantError: true,
			wantText:  "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			td, _, endpoint := startTestServer(t, tt.puller, nil, nil)
			session := connectClient(t, endpoint)

			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "pull_code",
				Arguments: map[string]any{"path": "/test/project"},
			})
			if err != nil {
				t.Fatalf("CallTool() protocol error: %v", err)
			}

			if result.IsError != tt.wantError {
				t.Errorf("IsError = %v, want %v", result.IsError, tt.wantError)
			}

			if !tt.puller.called {
				t.Error("puller.Pull was not called")
			}
			if tt.puller.gotName != "test-task" {
				t.Errorf("puller.gotName = %q, want %q", tt.puller.gotName, "test-task")
			}
			if tt.puller.gotPath != "/test/project" {
				t.Errorf("puller.gotPath = %q, want %q", tt.puller.gotPath, "/test/project")
			}
			wantLocal := filepath.Join(td.RepoPath())
			if tt.puller.gotLocal != wantLocal {
				t.Errorf("puller.gotLocal = %q, want %q", tt.puller.gotLocal, wantLocal)
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
		})
	}
}

func TestOpenPRTool(t *testing.T) {
	tests := []struct {
		name           string
		opener         *stubPROpener
		allowedRepos   []string
		input          map[string]any
		wantError      bool
		wantText       string
		wantForkCalled bool
	}{
		{
			name: "successful PR via fork",
			opener: &stubPROpener{
				user:     "testuser",
				forkName: "testuser/osbuild",
				prURL:    "https://github.com/osbuild/osbuild/pull/42",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"base":   "main",
				"title":  "Fix bug",
				"body":   "This fixes the bug",
			},
			wantText:       "https://github.com/osbuild/osbuild/pull/42",
			wantForkCalled: true,
		},
		{
			name: "user owns repo skips fork",
			opener: &stubPROpener{
				user:  "osbuild",
				prURL: "https://github.com/osbuild/osbuild/pull/99",
			},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"base":   "main",
				"title":  "Fix bug",
				"body":   "Direct push",
			},
			wantText:       "https://github.com/osbuild/osbuild/pull/99",
			wantForkCalled: false,
		},
		{
			name: "default base branch",
			opener: &stubPROpener{
				user:     "testuser",
				forkName: "testuser/osbuild",
				prURL:    "https://github.com/osbuild/osbuild/pull/43",
			},
			allowedRepos:   []string{"osbuild/osbuild"},
			wantForkCalled: true,
			input: map[string]any{
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "This fixes the bug",
			},
			wantText: "https://github.com/osbuild/osbuild/pull/43",
		},
		{
			name:         "repo not allowed",
			opener:       &stubPROpener{},
			allowedRepos: []string{"osbuild/*"},
			input: map[string]any{
				"repo":   "evil/repo",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "This fixes the bug",
			},
			wantError: true,
			wantText:  "not in the allowed repos list",
		},
		{
			name: "fork failure",
			opener: &stubPROpener{
				user:    "testuser",
				forkErr: fmt.Errorf("fork failed"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantForkCalled: true,
			input: map[string]any{
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError: true,
			wantText:  "fork failed",
		},
		{
			name: "push failure",
			opener: &stubPROpener{
				user:     "testuser",
				forkName: "testuser/osbuild",
				pushErr:  fmt.Errorf("push rejected"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantForkCalled: true,
			input: map[string]any{
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError: true,
			wantText:  "push rejected",
		},
		{
			name: "PR creation failure",
			opener: &stubPROpener{
				user:     "testuser",
				forkName: "testuser/osbuild",
				prErr:    fmt.Errorf("duplicate PR"),
			},
			allowedRepos:   []string{"osbuild/*"},
			wantForkCalled: true,
			input: map[string]any{
				"repo":   "osbuild/osbuild",
				"branch": "fix-bug",
				"title":  "Fix bug",
				"body":   "body",
			},
			wantError: true,
			wantText:  "duplicate PR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, endpoint := startTestServer(t, &stubPuller{}, tt.opener, tt.allowedRepos)
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

			// Verify default base branch
			if tt.name == "default base branch" && tt.opener.gotPRBase != "main" {
				t.Errorf("base = %q, want %q", tt.opener.gotPRBase, "main")
			}

			// Verify sourceRef includes task name
			if !tt.wantError && tt.opener.gotSource != "gjoll/test-task" {
				t.Errorf("sourceRef = %q, want %q", tt.opener.gotSource, "gjoll/test-task")
			}

			if tt.opener.forkCalled != tt.wantForkCalled {
				t.Errorf("forkCalled = %v, want %v", tt.opener.forkCalled, tt.wantForkCalled)
			}

			// When user owns repo, push target should be the upstream itself
			if tt.name == "user owns repo skips fork" && tt.opener.gotForkName != "osbuild/osbuild" {
				t.Errorf("pushTarget = %q, want %q", tt.opener.gotForkName, "osbuild/osbuild")
			}
		})
	}
}
