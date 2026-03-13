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

func startTestServer(t *testing.T, puller CodePuller) (*task.Dir, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	td, err := task.Create(dir, "test-task")
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := New(logger, "test-task", td, puller)
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
	_, _, endpoint := startTestServer(t, &stubPuller{})
	session := connectClient(t, endpoint)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}

	found := false
	for _, tool := range result.Tools {
		if tool.Name == "pull_code" {
			found = true
			break
		}
	}
	if !found {
		t.Error("pull_code tool not found in tool list")
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
			td, _, endpoint := startTestServer(t, tt.puller)
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
