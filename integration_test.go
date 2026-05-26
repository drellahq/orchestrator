//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drellabot/orchestrator/internal/gjoll"
	mcpserver "github.com/drellabot/orchestrator/internal/mcp"
	"github.com/drellabot/orchestrator/internal/task"
	"github.com/drellabot/orchestrator/internal/vcs"
)

const sandboxName = "orch-integ-test"

// testVCSProvider implements vcs.Provider for integration testing.
type testVCSProvider struct {
	trailerCalled bool
	gotTrailer    string

	lastCommentURL  string
	lastCommentBody string
}

func (t *testVCSProvider) AuthenticatedUser(_ context.Context) (string, error) {
	return "testuser", nil
}

func (t *testVCSProvider) EnsureFork(_ context.Context, upstream string) (string, error) {
	return "testuser/" + strings.SplitN(upstream, "/", 2)[1], nil
}

func (t *testVCSProvider) PushBranch(_ context.Context, repoDir, forkFullName, branch, sourceRef string) error {
	return nil
}

func (t *testVCSProvider) CreatePR(_ context.Context, upstream, forkOwner, branch, base, title, body string) (string, error) {
	return fmt.Sprintf("https://github.com/%s/pull/1", upstream), nil
}

func (t *testVCSProvider) AddCoAuthorTrailers(_ context.Context, repoDir, upstream, base, sourceRef, trailer string) error {
	t.trailerCalled = true
	t.gotTrailer = trailer
	return nil
}

func (t *testVCSProvider) CommentOnPR(_ context.Context, prURL, body string) error {
	t.lastCommentURL = prURL
	t.lastCommentBody = body
	return nil
}

func (t *testVCSProvider) CommentOnIssue(_ context.Context, repo string, issue int, body string) error {
	return nil
}

func (t *testVCSProvider) UpdatePRTitle(_ context.Context, prURL, title string) error {
	return nil
}

func (t *testVCSProvider) PostReview(_ context.Context, repo string, pr int, event, body string) error {
	return nil
}

func (t *testVCSProvider) IsPROpen(_ context.Context, repo string, prNumber int) (bool, error) {
	return true, nil
}

func (t *testVCSProvider) FetchAllComments(_ context.Context, repo string, prNumber int) ([]vcs.Comment, error) {
	return nil, nil
}

func (t *testVCSProvider) ReactToComment(_ context.Context, repo string, prNumber int, commentID int64, commentType vcs.CommentType, reaction string) error {
	return nil
}

func (t *testVCSProvider) ReactToIssue(_ context.Context, repo string, issueNumber int, reaction string) error {
	return nil
}

func (t *testVCSProvider) ListRepoFiles(_ context.Context, repo, branch, dir string) ([]string, error) {
	return nil, nil
}

func (t *testVCSProvider) GetFileContent(_ context.Context, repo, branch, path string) (string, error) {
	return "", nil
}

func (t *testVCSProvider) ListIssues(_ context.Context, repo string) ([]vcs.Issue, error) {
	return nil, nil
}

func (t *testVCSProvider) CloneRepo(_ context.Context, repo, dir string) error {
	return nil
}

func (t *testVCSProvider) PRNumberFromURL(url string) (int, error) {
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

func (t *testVCSProvider) RepoURL(repo string) string {
	return "https://github.com/" + repo + ".git"
}

// testTF returns the path to a minimal .tf file for integration testing.
// It installs git and a mock claude script via init_script.
func testTF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tfPath := filepath.Join(dir, "test-sandbox.tf")

	content := `terraform {
  required_providers {
    libvirt = { source = "dmacvicar/libvirt", version = "~> 0.9" }
  }
}

provider "libvirt" { uri = "qemu:///system" }

resource "libvirt_volume" "base" {
  name   = "fedora-base-${var.gjoll_name}.qcow2"
  pool   = "default"
  target = { format = { type = "qcow2" } }
  create = {
    content = {
      url = "https://download.fedoraproject.org/pub/fedora/linux/releases/43/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-43-1.6.x86_64.qcow2"
    }
  }
}

resource "libvirt_volume" "root" {
  name          = "root-${var.gjoll_name}.qcow2"
  pool          = "default"
  capacity      = 53687091200
  target        = { format = { type = "qcow2" } }
  backing_store = { path = libvirt_volume.base.path, format = { type = "qcow2" } }
}

resource "libvirt_cloudinit_disk" "init" {
  name = "cloudinit-${var.gjoll_name}.iso"
  meta_data = jsonencode({
    instance-id    = "gjoll-${var.gjoll_name}"
    local-hostname = "gjoll-${var.gjoll_name}"
  })
  user_data = <<-EOF
    #cloud-config
    users:
      - name: fedora
        sudo: ALL=(ALL) NOPASSWD:ALL
        shell: /bin/bash
        ssh_authorized_keys:
          - ${var.gjoll_ssh_pubkey}
  EOF
}

resource "libvirt_domain" "sandbox" {
  name        = "gjoll-${var.gjoll_name}"
  type        = "kvm"
  memory      = 4096
  memory_unit = "MiB"
  vcpu        = 2
  running     = var.gjoll_instance_state == "running"

  cpu = { mode = "host-passthrough" }
  os  = { type = "hvm" }

  devices = {
    disks = [
      {
        source = { file = { file = libvirt_volume.root.path } }
        target = { dev = "vda", bus = "virtio" }
        driver = { name = "qemu", type = "qcow2" }
      },
      {
        device = "cdrom"
        source = { file = { file = libvirt_cloudinit_disk.init.path } }
        target = { dev = "sda", bus = "sata" }
        driver = { name = "qemu", type = "raw" }
      },
    ]
    interfaces = [
      {
        source      = { network = { network = "default" } }
        model       = { type = "virtio" }
        wait_for_ip = var.gjoll_instance_state == "running" ? { source = "lease" } : null
      },
    ]
    consoles = [
      { target = { type = "serial", port = 0 } },
    ]
  }
}

data "libvirt_domain_interface_addresses" "sandbox" {
  count  = var.gjoll_instance_state == "running" ? 1 : 0
  domain = libvirt_domain.sandbox.name
  source = "lease"
}

output "public_ip" {
  value = var.gjoll_instance_state == "running" ? data.libvirt_domain_interface_addresses.sandbox[0].interfaces[0].addrs[0].addr : ""
}
output "instance_id" { value = tostring(libvirt_domain.sandbox.id) }
output "ssh_user"    { value = "fedora" }
output "init_script" {
  value = <<-EOT
    #!/bin/bash
    set -euo pipefail
    sudo dnf install -y git-core
  EOT
}
`
	if err := os.WriteFile(tfPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return tfPath
}

func TestIntegration(t *testing.T) {
	ctx := context.Background()
	runner := gjoll.New("")

	tfPath := testTF(t)

	// 1. Provision sandbox
	t.Log("Provisioning sandbox...")
	if err := runner.Up(ctx, sandboxName, tfPath); err != nil {
		t.Fatalf("gjoll up failed: %v", err)
	}

	tornDown := false
	t.Cleanup(func() {
		if tornDown {
			return
		}
		t.Log("Tearing down VM...")
		cmd := exec.Command("gjoll", "down", sandboxName)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("cleanup down failed: %v\n%s", err, out)
		}
	})

	// 2. Set up git inside the VM (using $HOME as the working directory)
	t.Log("Setting up git in sandbox...")
	if err := runner.SSH(ctx, sandboxName, "git config --global user.name Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if err := runner.SSH(ctx, sandboxName, "git config --global user.email test@test.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if err := runner.SSH(ctx, sandboxName, "cd ~ && git init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// 3. Create a file, commit it on the VM
	t.Log("Creating test commit in sandbox...")
	if err := runner.SSH(ctx, sandboxName,
		"echo 'hello from integration test' > ~/hello.txt && cd ~ && git add -A && git commit -m 'test commit'"); err != nil {
		t.Fatalf("creating test commit: %v", err)
	}

	// 4. Create task directory and start MCP server
	outputDir := t.TempDir()
	taskDir, err := task.Create(outputDir, sandboxName)
	if err != nil {
		t.Fatalf("task.Create: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	provider := &testVCSProvider{}
	mcpSrv := mcpserver.New(logger, sandboxName, taskDir, runner, provider, []string{"test/*"}, "", "")
	if err := mcpSrv.Start(); err != nil {
		t.Fatalf("MCP server start: %v", err)
	}
	defer func() { _ = mcpSrv.Stop(ctx) }()

	// 5. Call open_pr from inside the VM via ssh -R tunnel
	// The MCP server listens on a dynamic host port; we reverse-tunnel it
	// to MCPRemotePort inside the VM so curl can reach it.
	t.Log("Calling open_pr from sandbox via ssh -R tunnel...")
	mcpPort := mcpSrv.Port()
	mcpTunnel := fmt.Sprintf("%d:localhost:%d", mcpserver.MCPRemotePort, mcpPort)
	remotePort := mcpserver.MCPRemotePort
	pullScript := fmt.Sprintf(`set -e
HEADERS=$(curl -s -D - -o /dev/null -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1.0"}}}')
SESSION_ID=$(echo "$HEADERS" | grep -i "mcp-session-id" | tr -d '\r' | awk '{print $2}')
echo "Session ID: $SESSION_ID"
curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
RESULT=$(curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"open_pr","arguments":{"path":"~","repo":"test/repo","branch":"test-branch","title":"Test","body":"Test"}}}')
echo "Result: $RESULT"`, remotePort, remotePort, remotePort)
	if err := runner.SSHProxy(ctx, sandboxName, &gjoll.SSHOpts{ReverseTunnels: []string{mcpTunnel}}, pullScript); err != nil {
		t.Fatalf("open_pr via ssh -R: %v", err)
	}

	// 6. Verify code was pulled
	t.Log("Verifying pulled code...")
	repoDir := taskDir.RepoPath()

	// Check that the git repo exists and has the gjoll remote branch
	gitCmd := exec.Command("git", "branch", "-a")
	gitCmd.Dir = repoDir
	branchOut, err := gitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch -a in repo: %v\n%s", err, branchOut)
	}
	t.Logf("Branches: %s", branchOut)

	expectedBranch := fmt.Sprintf("gjoll-%s", sandboxName)
	if !strings.Contains(string(branchOut), expectedBranch) {
		t.Errorf("expected branch %q not found in:\n%s", expectedBranch, branchOut)
	}

	// Check the file content
	showCmd := exec.Command("git", "show", expectedBranch+":hello.txt")
	showCmd.Dir = repoDir
	showOut, err := showCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show hello.txt: %v\n%s", err, showOut)
	}
	if !strings.Contains(string(showOut), "hello from integration test") {
		t.Errorf("unexpected file content: %q", showOut)
	}

	// 6a. Verify PR was recorded in task state
	t.Log("Verifying PR recorded in task state...")
	state, err := taskDir.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if len(state.Resources.GitHub.PRs) != 1 {
		t.Fatalf("expected 1 PR in state, got %d", len(state.Resources.GitHub.PRs))
	}
	pr := state.Resources.GitHub.PRs[0]
	if pr.URL != "https://github.com/test/repo/pull/1" {
		t.Errorf("PR URL = %q, want %q", pr.URL, "https://github.com/test/repo/pull/1")
	}
	if pr.Repo != "test/repo" {
		t.Errorf("PR Repo = %q, want %q", pr.Repo, "test/repo")
	}
	if pr.Branch != "test-branch" {
		t.Errorf("PR Branch = %q, want %q", pr.Branch, "test-branch")
	}
	if pr.Base != "main" {
		t.Errorf("PR Base = %q, want %q", pr.Base, "main")
	}

	// 6b. Test comment_on_pr from inside the VM
	t.Log("Calling comment_on_pr from sandbox via ssh -R tunnel...")
	commentScript := fmt.Sprintf(`set -e
HEADERS=$(curl -s -D - -o /dev/null -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1.0"}}}')
SESSION_ID=$(echo "$HEADERS" | grep -i "mcp-session-id" | tr -d '\r' | awk '{print $2}')
echo "Session ID: $SESSION_ID"
curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
RESULT=$(curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"comment_on_pr","arguments":{"pr_url":"https://github.com/test/repo/pull/1","body":"Pushed updated code"}}}')
echo "Result: $RESULT"`, remotePort, remotePort, remotePort)
	if err := runner.SSHProxy(ctx, sandboxName, &gjoll.SSHOpts{ReverseTunnels: []string{mcpTunnel}}, commentScript); err != nil {
		t.Fatalf("comment_on_pr via ssh -R: %v", err)
	}

	// Verify the comment was dispatched to the VCS provider
	if provider.lastCommentURL != "https://github.com/test/repo/pull/1" {
		t.Errorf("comment URL = %q, want %q", provider.lastCommentURL, "https://github.com/test/repo/pull/1")
	}
	if provider.lastCommentBody != "Pushed updated code" {
		t.Errorf("comment body = %q, want %q", provider.lastCommentBody, "Pushed updated code")
	}

	// 6c. Verify comment_on_pr rejects unowned PRs
	t.Log("Verifying comment_on_pr rejects unowned PRs...")
	rejectScript := fmt.Sprintf(`set -e
HEADERS=$(curl -s -D - -o /dev/null -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1.0"}}}')
SESSION_ID=$(echo "$HEADERS" | grep -i "mcp-session-id" | tr -d '\r' | awk '{print $2}')
curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
RESULT=$(curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"comment_on_pr","arguments":{"pr_url":"https://github.com/evil/repo/pull/99","body":"sneaky"}}}')
echo "Result: $RESULT"
echo "$RESULT" | grep -q "was not opened by this task"`, remotePort, remotePort, remotePort)
	if err := runner.SSHProxy(ctx, sandboxName, &gjoll.SSHOpts{ReverseTunnels: []string{mcpTunnel}}, rejectScript); err != nil {
		t.Fatalf("comment_on_pr rejection test via ssh -R: %v", err)
	}

	// 7. Test transcript streaming via SSHProxyOutput
	t.Log("Testing transcript streaming...")
	transcriptContent := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from test"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write"}]}}
{"type":"result","subtype":"success"}
`
	if err := runner.SSH(ctx, sandboxName, fmt.Sprintf("cat > ~/transcript.jsonl << 'JSONL'\n%sJSONL", transcriptContent)); err != nil {
		t.Fatalf("creating fake transcript: %v", err)
	}

	var buf bytes.Buffer
	if err := runner.SSHProxyOutput(ctx, sandboxName, &buf, nil, "cat ~/transcript.jsonl"); err != nil {
		t.Fatalf("SSHProxyOutput: %v", err)
	}
	gotOutput := buf.String()
	// SSHProxyOutput captures raw stdout — verify the JSONL comes through
	for _, want := range []string{`"type":"assistant"`, `"tool_use"`, `"result"`} {
		if !strings.Contains(gotOutput, want) {
			t.Errorf("SSHProxyOutput missing %q in output:\n%s", want, gotOutput)
		}
	}

	// 7a. Copy transcript to task directory
	t.Log("Testing transcript copy...")
	if err := runner.Cp(ctx, sandboxName, ":~/transcript.jsonl", taskDir.TranscriptPath()); err != nil {
		t.Fatalf("copying transcript: %v", err)
	}
	transcriptData, err := os.ReadFile(taskDir.TranscriptPath())
	if err != nil {
		t.Fatalf("reading local transcript: %v", err)
	}
	if !strings.Contains(string(transcriptData), `"type":"assistant"`) {
		t.Errorf("transcript content mismatch: %q", transcriptData)
	}

	// 8. Copy conversations directory (testing gjoll cp)
	t.Log("Testing conversation copy...")
	if err := runner.SSH(ctx, sandboxName, "mkdir -p ~/.claude && echo test > ~/.claude/test.json"); err != nil {
		t.Fatalf("creating fake conversation: %v", err)
	}
	if err := runner.Cp(ctx, sandboxName, ":~/.claude/", taskDir.ConversationsPath()); err != nil {
		t.Fatalf("copying conversations: %v", err)
	}

	convFiles, err := os.ReadDir(taskDir.ConversationsPath())
	if err != nil {
		t.Fatalf("reading conversations dir: %v", err)
	}
	if len(convFiles) == 0 {
		t.Error("no files in conversations directory after copy")
	}

	// 9. Stop sandbox
	t.Log("Stopping sandbox...")
	// Sync disk before stopping to ensure data is persisted
	_ = runner.SSH(ctx, sandboxName, "sync")
	if err := runner.Stop(ctx, sandboxName); err != nil {
		t.Fatalf("gjoll stop: %v", err)
	}

	// Give it a moment to fully stop
	time.Sleep(5 * time.Second)

	// 10. Resume stopped sandbox and verify state persisted
	t.Log("Resuming stopped sandbox...")
	if err := runner.Start(ctx, sandboxName); err != nil {
		t.Fatalf("gjoll start (resume) failed: %v", err)
	}

	t.Log("Verifying state persisted after resume...")
	if err := runner.SSH(ctx, sandboxName, "test -f ~/hello.txt"); err != nil {
		t.Fatalf("hello.txt does not exist after resume: %v", err)
	}

	t.Log("Stopping sandbox again...")
	if err := runner.Stop(ctx, sandboxName); err != nil {
		t.Fatalf("gjoll stop (2nd): %v", err)
	}
	time.Sleep(5 * time.Second)

	// 11. Tear down
	t.Log("Tearing down sandbox...")
	if err := runner.Down(ctx, sandboxName); err != nil {
		t.Fatalf("gjoll down: %v", err)
	}
	tornDown = true

	t.Log("Integration test passed!")
}

func TestIntegrationWithAuthor(t *testing.T) {
	ctx := context.Background()
	runner := gjoll.New("")

	tfPath := testTF(t)

	const authorSandboxName = "orch-integ-author"

	t.Log("Provisioning sandbox for author test...")
	if err := runner.Up(ctx, authorSandboxName, tfPath); err != nil {
		t.Fatalf("gjoll up failed: %v", err)
	}

	tornDown := false
	t.Cleanup(func() {
		if tornDown {
			return
		}
		t.Log("Tearing down author test VM...")
		cmd := exec.Command("gjoll", "down", authorSandboxName)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("cleanup down failed: %v\n%s", err, out)
		}
	})

	// Set up git inside the VM
	t.Log("Setting up git in sandbox...")
	if err := runner.SSH(ctx, authorSandboxName, "git config --global user.name Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if err := runner.SSH(ctx, authorSandboxName, "git config --global user.email test@test.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if err := runner.SSH(ctx, authorSandboxName, "cd ~ && git init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := runner.SSH(ctx, authorSandboxName,
		"echo 'hello' > ~/hello.txt && cd ~ && git add -A && git commit -m 'test commit'"); err != nil {
		t.Fatalf("creating test commit: %v", err)
	}

	// Create task directory and start MCP server with author
	outputDir := t.TempDir()
	taskDir, err := task.Create(outputDir, authorSandboxName)
	if err != nil {
		t.Fatalf("task.Create: %v", err)
	}

	provider := &testVCSProvider{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mcpSrv := mcpserver.New(logger, authorSandboxName, taskDir, runner, provider, []string{"test/*"}, "Test User <test@example.com>", "")
	if err := mcpSrv.Start(); err != nil {
		t.Fatalf("MCP server start: %v", err)
	}
	defer func() { _ = mcpSrv.Stop(ctx) }()

	// Call open_pr via ssh -R tunnel
	t.Log("Calling open_pr with author from sandbox...")
	mcpPort := mcpSrv.Port()
	mcpTunnel := fmt.Sprintf("%d:localhost:%d", mcpserver.MCPRemotePort, mcpPort)
	remotePort := mcpserver.MCPRemotePort
	pullScript := fmt.Sprintf(`set -e
HEADERS=$(curl -s -D - -o /dev/null -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1.0"}}}')
SESSION_ID=$(echo "$HEADERS" | grep -i "mcp-session-id" | tr -d '\r' | awk '{print $2}')
curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
RESULT=$(curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"open_pr","arguments":{"path":"~","repo":"test/repo","branch":"author-test","title":"Test","body":"Test"}}}')
echo "Result: $RESULT"`, remotePort, remotePort, remotePort)
	if err := runner.SSHProxy(ctx, authorSandboxName, &gjoll.SSHOpts{ReverseTunnels: []string{mcpTunnel}}, pullScript); err != nil {
		t.Fatalf("open_pr via ssh -R: %v", err)
	}

	// Verify AddCoAuthorTrailers was called
	if !provider.trailerCalled {
		t.Error("AddCoAuthorTrailers was not called")
	}
	wantTrailer := "Co-authored-by: Test User <test@example.com>"
	if provider.gotTrailer != wantTrailer {
		t.Errorf("trailer = %q, want %q", provider.gotTrailer, wantTrailer)
	}

	// Verify author is persisted in task state so task continue can use it
	t.Log("Saving author to task state and verifying persistence...")
	if err := taskDir.SaveMetadata(authorSandboxName, "test task", "Test User <test@example.com>", time.Now()); err != nil {
		t.Fatalf("SaveMetadata() error: %v", err)
	}
	state, err := taskDir.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if state.Author != "Test User <test@example.com>" {
		t.Errorf("persisted Author = %q, want %q", state.Author, "Test User <test@example.com>")
	}

	// Verify a new MCP server created with the persisted author works correctly
	// (simulates what task continue would do: load author from state, pass to MCP server)
	provider2 := &testVCSProvider{}
	mcpSrv2 := mcpserver.New(logger, authorSandboxName, taskDir, runner, provider2, []string{"test/*"}, state.Author, "")
	if err := mcpSrv2.Start(); err != nil {
		t.Fatalf("MCP server 2 start: %v", err)
	}
	defer func() { _ = mcpSrv2.Stop(ctx) }()

	mcpPort2 := mcpSrv2.Port()
	mcpTunnel2 := fmt.Sprintf("%d:localhost:%d", mcpserver.MCPRemotePort, mcpPort2)
	pullScript2 := fmt.Sprintf(`set -e
HEADERS=$(curl -s -D - -o /dev/null -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1.0"}}}')
SESSION_ID=$(echo "$HEADERS" | grep -i "mcp-session-id" | tr -d '\r' | awk '{print $2}')
curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
RESULT=$(curl -s -X POST http://localhost:%d/ -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -H "Mcp-Session-Id: $SESSION_ID" -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"open_pr","arguments":{"path":"~","repo":"test/repo","branch":"continue-test","title":"Continue Test","body":"Test"}}}')
echo "Result: $RESULT"`, remotePort, remotePort, remotePort)
	if err := runner.SSHProxy(ctx, authorSandboxName, &gjoll.SSHOpts{ReverseTunnels: []string{mcpTunnel2}}, pullScript2); err != nil {
		t.Fatalf("open_pr via ssh -R (continue simulation): %v", err)
	}

	if !provider2.trailerCalled {
		t.Error("AddCoAuthorTrailers was not called on continued task")
	}
	if provider2.gotTrailer != wantTrailer {
		t.Errorf("continued task trailer = %q, want %q", provider2.gotTrailer, wantTrailer)
	}

	// Tear down
	t.Log("Tearing down author test sandbox...")
	if err := runner.Down(ctx, authorSandboxName); err != nil {
		t.Fatalf("gjoll down: %v", err)
	}
	tornDown = true

	t.Log("Author integration test passed!")
}
