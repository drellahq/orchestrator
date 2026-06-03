package profile

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/drellabot/orchestrator/internal/agent"
	"github.com/drellabot/orchestrator/internal/sandbox"
)

// Apply writes the profile's configuration into a sandbox.
//
// It performs the following steps (skipping optional files that are absent):
//  1. Write base prompt + profile CLAUDE.md → ~/<configDir>/CLAUDE.md
//  2. Copy settings.json → ~/<configDir>/settings.json
//  3. Register MCP servers from mcp.yaml via the agent backend
//  4. Run setup.sh on the host with helper scripts and environment variables
func Apply(ctx context.Context, p *Profile, runner sandbox.Runner, backend agent.Backend, sbx string, taskDir string, basePrompt string, vars map[string]string) error {
	home := runner.UserHome()
	configDir := backend.ConfigDir()

	// 1. Write combined CLAUDE.md
	claudemd := basePrompt + "\n\n# Profile: " + p.Name + "\n\n" + p.Claudemd
	if err := writeToSandbox(ctx, runner, backend, sbx, claudemd, sbx+":"+home+"/"+configDir+"/CLAUDE.md"); err != nil {
		return fmt.Errorf("writing CLAUDE.md: %w", err)
	}

	// 2. Copy settings.json if present
	if p.Settings != "" {
		if err := runner.Cp(ctx, sbx, p.Settings, sbx+":"+home+"/"+configDir+"/settings.json"); err != nil {
			return fmt.Errorf("copying settings.json: %w", err)
		}
		slog.Debug("Copied profile settings.json", "profile", p.Name)
	}

	// Fix ownership of copied files (podman cp runs as root; on gjoll this
	// is a harmless no-op error since the SSH user already owns the files).
	fixCmd := "chown -R $(stat -c '%U:%G' " + home + ") " + home + "/" + configDir + " 2>/dev/null || true"
	_ = runner.SSH(ctx, sbx, "bash", "-c", fixCmd)

	// 3. Register MCP servers from mcp.yaml
	if p.MCP != nil {
		for _, server := range p.MCP.Servers {
			if err := registerMCPServer(ctx, runner, backend, sbx, server); err != nil {
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
func writeToSandbox(ctx context.Context, runner sandbox.Runner, backend agent.Backend, sbx, content, dest string) error {
	configDir := backend.ConfigDir()
	runner.SSH(ctx, sbx, "bash", "-c", runner.AsUser("mkdir -p ~/"+configDir))

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

// registerMCPServer registers an MCP server in the sandbox using the agent backend.
func registerMCPServer(ctx context.Context, runner sandbox.Runner, backend agent.Backend, sbx string, server MCPServer) error {
	var innerCmd string
	switch server.Transport {
	case "http":
		innerCmd = backend.MCPAddCmd(server.Name, server.Transport, server.URL, server.Scope)
	case "stdio":
		innerCmd = backend.MCPAddCmd(server.Name, server.Transport, server.Command, server.Scope)
	default:
		return fmt.Errorf("unsupported transport %q", server.Transport)
	}
	return runner.SSH(ctx, sbx, "bash", "-c", runner.AsUser(innerCmd))
}

// runSetup executes setup.sh on the host with helper scripts on PATH.
func runSetup(ctx context.Context, runner sandbox.Runner, sbx, setupPath, taskDir string, vars map[string]string) error {
	helpersDir, err := os.MkdirTemp("", "profile-helpers-*")
	if err != nil {
		return fmt.Errorf("creating helpers dir: %w", err)
	}
	defer os.RemoveAll(helpersDir)

	cpScript, sshScript := runner.HelperScripts(sbx)

	if err := os.WriteFile(filepath.Join(helpersDir, "sandbox-cp"), []byte(cpScript), 0755); err != nil {
		return fmt.Errorf("writing sandbox-cp: %w", err)
	}

	if err := os.WriteFile(filepath.Join(helpersDir, "sandbox-ssh"), []byte(sshScript), 0755); err != nil {
		return fmt.Errorf("writing sandbox-ssh: %w", err)
	}

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
