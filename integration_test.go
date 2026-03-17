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
	"strings"
	"testing"
	"time"

	"github.com/drellabot/orchestrator/internal/gjoll"
	mcpserver "github.com/drellabot/orchestrator/internal/mcp"
	"github.com/drellabot/orchestrator/internal/task"
)

const sandboxName = "orch-integ-test"

// testPROpener implements mcpserver.PROpener for integration testing.
type testPROpener struct {
	trailerCalled bool
	gotTrailer    string
}

func (t *testPROpener) AuthenticatedUser(_ context.Context) (string, error) {
	return "testuser", nil
}

func (t *testPROpener) EnsureFork(_ context.Context, upstream string) (string, error) {
	return "testuser/" + strings.SplitN(upstream, "/", 2)[1], nil
}

func (t *testPROpener) PushBranch(_ context.Context, repoDir, forkFullName, branch, sourceRef string) error {
	return nil
}

func (t *testPROpener) CreatePR(_ context.Context, upstream, forkOwner, branch, base, title, body string) (string, error) {
	return fmt.Sprintf("https://github.com/%s/pull/1", upstream), nil
}

func (t *testPROpener) AddCoAuthorTrailers(_ context.Context, repoDir, upstream, base, sourceRef, trailer string) error {
	t.trailerCalled = true
	t.gotTrailer = trailer
	return nil
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
	mcpSrv := mcpserver.New(logger, sandboxName, taskDir, runner, &testPROpener{}, []string{"test/*"}, "")
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

	expectedBranch := fmt.Sprintf("gjoll/%s", sandboxName)
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

	prOpener := &testPROpener{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mcpSrv := mcpserver.New(logger, authorSandboxName, taskDir, runner, prOpener, []string{"test/*"}, "Test User <test@example.com>")
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
	if !prOpener.trailerCalled {
		t.Error("AddCoAuthorTrailers was not called")
	}
	wantTrailer := "Co-authored-by: Test User <test@example.com>"
	if prOpener.gotTrailer != wantTrailer {
		t.Errorf("trailer = %q, want %q", prOpener.gotTrailer, wantTrailer)
	}

	// Tear down
	t.Log("Tearing down author test sandbox...")
	if err := runner.Down(ctx, authorSandboxName); err != nil {
		t.Fatalf("gjoll down: %v", err)
	}
	tornDown = true

	t.Log("Author integration test passed!")
}
