package profile

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/drellabot/orchestrator/internal/sandbox"
)

// Apply writes the profile's configuration into a sandbox.
//
// It performs the following steps (skipping optional files that are absent):
//  1. Write base prompt + profile CLAUDE.md → ~/.claude/CLAUDE.md
//  2. Copy settings.json → ~/.claude/settings.json
//  3. Register MCP servers from mcp.yaml via "claude mcp add"
//  4. Run setup.sh on the host with helper scripts and environment variables
func Apply(ctx context.Context, p *Profile, runner sandbox.Runner, sbx string, taskDir string, basePrompt string, vars map[string]string) error {
	// 1. Write combined CLAUDE.md
	claudemd := basePrompt + "\n\n# Profile: " + p.Name + "\n\n" + p.Claudemd
	if err := writeToSandbox(ctx, runner, sbx, claudemd, ":~/.claude/CLAUDE.md"); err != nil {
		return fmt.Errorf("writing CLAUDE.md: %w", err)
	}

	// 2. Copy settings.json if present
	if p.Settings != "" {
		if err := runner.Cp(ctx, sbx, p.Settings, ":~/.claude/settings.json"); err != nil {
			return fmt.Errorf("copying settings.json: %w", err)
		}
		slog.Debug("Copied profile settings.json", "profile", p.Name)
	}

	// 3. Register MCP servers from mcp.yaml
	if p.MCP != nil {
		for _, server := range p.MCP.Servers {
			if err := registerMCPServer(ctx, runner, sbx, server); err != nil {
				return fmt.Errorf("registering MCP server %q: %w", server.Name, err)
			}
			slog.Debug("Registered MCP server", "profile", p.Name, "server", server.Name)
		}
	}

	// 4. Run setup.sh on the host
	if p.Setup != "" {
		if err := runSetup(ctx, runner, sbx, p.Setup, taskDir, vars); err != nil {
			return fmt.Errorf("running setup.sh: %w", err)
		}
		slog.Debug("Ran profile setup.sh", "profile", p.Name)
	}

	return nil
}

// writeToSandbox writes content to a file in the sandbox via a temp file + cp.
func writeToSandbox(ctx context.Context, runner sandbox.Runner, sbx, content, dest string) error {
	// Ensure the parent directory exists in the sandbox
	if err := runner.SSH(ctx, sbx, "mkdir", "-p", "~/.claude"); err != nil {
		return fmt.Errorf("creating ~/.claude directory: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "profile-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	return runner.Cp(ctx, sbx, tmpFile.Name(), dest)
}

// registerMCPServer runs "claude mcp add" in the sandbox for a single server.
// Commands are passed as separate args to runner.SSH which handles user context
// (gjoll runs as the SSH user directly; podman wraps in su - claude).
func registerMCPServer(ctx context.Context, runner sandbox.Runner, sbx string, server MCPServer) error {
	var args []string
	switch server.Transport {
	case "stdio":
		args = []string{"claude", "mcp", "add", "--transport", "stdio"}
		if server.Scope != "" {
			args = append(args, "--scope", server.Scope)
		}
		args = append(args, server.Name, server.Command)
		args = append(args, server.Args...)
	case "http":
		args = []string{"claude", "mcp", "add", "--transport", "http"}
		if server.Scope != "" {
			args = append(args, "--scope", server.Scope)
		}
		args = append(args, server.Name, server.URL)
	default:
		return fmt.Errorf("unsupported MCP transport type: %q", server.Transport)
	}
	return runner.SSH(ctx, sbx, args...)
}

// runSetup executes setup.sh on the host with helper scripts on PATH.
func runSetup(ctx context.Context, runner sandbox.Runner, sbx, setupPath, taskDir string, vars map[string]string) error {
	// Create a temp directory for helper scripts
	helpersDir, err := os.MkdirTemp("", "profile-helpers-*")
	if err != nil {
		return fmt.Errorf("creating helpers dir: %w", err)
	}
	defer os.RemoveAll(helpersDir)

	// Generate backend-appropriate helper scripts.
	// The podman sandbox-cp translates the ":path" convention used by
	// runner.Cp() (e.g. ":~/file" or ":/abs/path") to podman's "name:path"
	// format with tilde expansion, and chowns files to the claude user.
	var sandboxCp, sandboxSSH string
	switch runner.(type) {
	case *sandbox.PodmanRunner:
		sandboxCp = fmt.Sprintf(`#!/bin/bash
set -euo pipefail
CONTAINER=%q

translate_path() {
  local p="$1"
  if [[ "$p" == :~/* ]]; then
    echo "${CONTAINER}:/home/claude/${p#:~/}"
  elif [[ "$p" == :~ ]]; then
    echo "${CONTAINER}:/home/claude"
  elif [[ "$p" == :* ]]; then
    echo "${CONTAINER}:${p#:}"
  else
    echo "$p"
  fi
}

src=$(translate_path "$1")
dest=$(translate_path "$2")
podman cp "$src" "$dest"

# Chown to claude user when copying TO the container
if [[ "$2" == :* ]]; then
  remote="${2#:}"
  [[ "$remote" == ~/* ]] && remote="/home/claude/${remote#~/}"
  [[ "$remote" == "~" ]] && remote="/home/claude"
  podman exec %q chown -R claude:claude "$remote" 2>/dev/null || true
fi
`, sbx, sbx)
		// Wrap in su - claude to match runner.SSH() behavior — podman exec
		// runs as root by default, but setup scripts expect the claude user.
		sandboxSSH = fmt.Sprintf(`#!/bin/bash
set -euo pipefail
podman exec %q su - claude -c "$(printf '%%q ' "$@")"
`, sbx)
	default:
		// gjoll backend (default)
		sandboxCp = fmt.Sprintf(`#!/bin/bash
set -euo pipefail
gjoll cp %q "$1" "$2"
`, sbx)
		sandboxSSH = fmt.Sprintf(`#!/bin/bash
set -euo pipefail
gjoll ssh %q -- "$@"
`, sbx)
	}

	// Write sandbox-cp helper
	if err := os.WriteFile(filepath.Join(helpersDir, "sandbox-cp"), []byte(sandboxCp), 0755); err != nil {
		return fmt.Errorf("writing sandbox-cp: %w", err)
	}

	// Write sandbox-ssh helper
	if err := os.WriteFile(filepath.Join(helpersDir, "sandbox-ssh"), []byte(sandboxSSH), 0755); err != nil {
		return fmt.Errorf("writing sandbox-ssh: %w", err)
	}

	// Build environment
	env := os.Environ()
	env = append(env,
		"SANDBOX="+sbx,
		"TASK_DIR="+taskDir,
		"PATH="+helpersDir+":"+os.Getenv("PATH"),
	)
	for k, v := range vars {
		env = append(env, k+"="+v)
	}

	cmd := exec.CommandContext(ctx, "bash", setupPath)
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("setup.sh failed: %w", err)
	}

	return nil
}
