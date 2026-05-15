package profile

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drellabot/orchestrator/internal/shellutil"

	"github.com/drellabot/orchestrator/internal/gjoll"
)

// Apply writes the profile's configuration into a sandbox.
//
// It performs the following steps (skipping optional files that are absent):
//  1. Write base prompt + profile CLAUDE.md → ~/.claude/CLAUDE.md
//  2. Copy settings.json → ~/.claude/settings.json
//  3. Register MCP servers from mcp.yaml via "claude mcp add"
//  4. Run setup.sh on the host with helper scripts and environment variables
func Apply(ctx context.Context, p *Profile, runner *gjoll.Runner, sandbox string, taskDir string, basePrompt string, vars map[string]string) error {
	// 1. Write combined CLAUDE.md
	claudemd := basePrompt + "\n\n# Profile: " + p.Name + "\n\n" + p.Claudemd
	if err := writeToSandbox(ctx, runner, sandbox, claudemd, ":~/.claude/CLAUDE.md"); err != nil {
		return fmt.Errorf("writing CLAUDE.md: %w", err)
	}

	// 2. Copy settings.json if present
	if p.Settings != "" {
		if err := runner.Cp(ctx, sandbox, p.Settings, ":~/.claude/settings.json"); err != nil {
			return fmt.Errorf("copying settings.json: %w", err)
		}
		slog.Debug("Copied profile settings.json", "profile", p.Name)
	}

	// 3. Register MCP servers from mcp.yaml
	if p.MCP != nil {
		for _, server := range p.MCP.Servers {
			if err := registerMCPServer(ctx, runner, sandbox, server); err != nil {
				return fmt.Errorf("registering MCP server %q: %w", server.Name, err)
			}
			slog.Debug("Registered MCP server", "profile", p.Name, "server", server.Name)
		}
	}

	// 4. Run setup.sh on the host
	if p.Setup != "" {
		if err := runSetup(ctx, runner, sandbox, p.Setup, taskDir, vars); err != nil {
			return fmt.Errorf("running setup.sh: %w", err)
		}
		slog.Debug("Ran profile setup.sh", "profile", p.Name)
	}

	return nil
}

// writeToSandbox writes content to a file in the sandbox via a temp file + cp.
func writeToSandbox(ctx context.Context, runner *gjoll.Runner, sandbox, content, dest string) error {
	// Ensure the parent directory exists in the sandbox
	runner.SSH(ctx, sandbox, "mkdir -p ~/.claude")

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

	return runner.Cp(ctx, sandbox, tmpFile.Name(), dest)
}

// registerMCPServer runs "claude mcp add" in the sandbox for a single server.
func registerMCPServer(ctx context.Context, runner *gjoll.Runner, sandbox string, server MCPServer) error {
	var args []string
	switch server.Transport {
	case "stdio":
		args = []string{"claude", "mcp", "add", "--transport", "stdio"}
		if server.Scope != "" {
			args = append(args, "--scope", shellutil.Quote(server.Scope))
		}
		args = append(args, shellutil.Quote(server.Name), shellutil.Quote(server.Command))
		for _, a := range server.Args {
			args = append(args, shellutil.Quote(a))
		}
	case "http":
		args = []string{"claude", "mcp", "add", "--transport", "http"}
		if server.Scope != "" {
			args = append(args, "--scope", shellutil.Quote(server.Scope))
		}
		args = append(args, shellutil.Quote(server.Name), shellutil.Quote(server.URL))
	}
	return runner.SSH(ctx, sandbox, strings.Join(args, " "))
}

// runSetup executes setup.sh on the host with helper scripts on PATH.
func runSetup(ctx context.Context, runner *gjoll.Runner, sandbox, setupPath, taskDir string, vars map[string]string) error {
	// Create a temp directory for helper scripts
	helpersDir, err := os.MkdirTemp("", "profile-helpers-*")
	if err != nil {
		return fmt.Errorf("creating helpers dir: %w", err)
	}
	defer os.RemoveAll(helpersDir)

	gjollBin := "gjoll"

	// Write sandbox-cp helper
	sandboxCp := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
%s cp %s "$1" "$2"
`, gjollBin, shellutil.Quote(sandbox))
	if err := os.WriteFile(filepath.Join(helpersDir, "sandbox-cp"), []byte(sandboxCp), 0755); err != nil {
		return fmt.Errorf("writing sandbox-cp: %w", err)
	}

	// Write sandbox-ssh helper
	sandboxSSH := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
%s ssh %s -- "$@"
`, gjollBin, shellutil.Quote(sandbox))
	if err := os.WriteFile(filepath.Join(helpersDir, "sandbox-ssh"), []byte(sandboxSSH), 0755); err != nil {
		return fmt.Errorf("writing sandbox-ssh: %w", err)
	}

	// Build environment
	env := os.Environ()
	env = append(env,
		"SANDBOX="+sandbox,
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
