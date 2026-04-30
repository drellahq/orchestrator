package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PodmanRunner implements Runner using podman containers as sandboxes.
//
// Podman containers run as root, but Claude is installed under a non-root
// "claude" user. SSH/SSHProxy/SSHProxyOutput automatically wrap commands
// in "su - claude -c ..." so callers don't need to be backend-aware.
// Commands that need root (used internally during provisioning) use the
// lower-level run/runInteractive methods directly.
type PodmanRunner struct {
	image        string
	anthropicKey string
}

// NewPodman creates a PodmanRunner.
// mcpPort is accepted for interface compatibility but unused — podman containers
// use --network host, so the MCP server is reachable without port mapping.
func NewPodman(image, anthropicKeyFile string, mcpPort int) *PodmanRunner {
	if image == "" {
		image = "fedora:43"
	}
	return &PodmanRunner{
		image:        image,
		anthropicKey: anthropicKeyFile,
	}
}

// Up provisions a new container sandbox.
func (r *PodmanRunner) Up(ctx context.Context, name string, config string) error {
	args := []string{
		"run", "-d",
		"--name", name,
		"--network", "host",
		"--security-opt", "label=disable",
	}

	args = append(args, r.image, "sleep", "infinity")

	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman run: %w", err)
	}

	// Create non-root user for Claude (runs as root)
	userSetupCmds := []string{
		"bash", "-c",
		"useradd -m -s /bin/bash claude && dnf install -y git-core curl sudo && chown claude:claude /home/claude && (chown -R claude:claude /home/claude 2>/dev/null || true)",
	}
	if err := r.SSHAsRoot(ctx, name, userSetupCmds...); err != nil {
		_ = r.Down(context.Background(), name)
		return fmt.Errorf("user setup: %w", err)
	}

	// Install Claude Code as the claude user (runs as root, delegates to su)
	installCmds := []string{
		"bash", "-c",
		"su - claude -c 'curl -fsSL https://claude.ai/install.sh | bash'",
	}
	if err := r.SSHAsRoot(ctx, name, installCmds...); err != nil {
		_ = r.Down(context.Background(), name)
		return fmt.Errorf("claude install: %w", err)
	}

	// Copy and configure API key
	if r.anthropicKey != "" {
		keyPath := r.anthropicKey
		if strings.HasPrefix(keyPath, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				_ = r.Down(context.Background(), name)
				return fmt.Errorf("resolving home directory for API key path: %w", err)
			}
			keyPath = filepath.Join(home, keyPath[2:])
		}

		// Create .anthropic directory and copy key (as root)
		mkdirCmd := []string{"bash", "-c", "mkdir -p /home/claude/.anthropic && chown claude:claude /home/claude/.anthropic"}
		if err := r.SSHAsRoot(ctx, name, mkdirCmd...); err != nil {
			_ = r.Down(context.Background(), name)
			return fmt.Errorf("creating .anthropic dir: %w", err)
		}

		// Copy API key (Cp auto-chowns to claude user)
		if err := r.Cp(ctx, name, keyPath, ":/home/claude/.anthropic/api_key"); err != nil {
			_ = r.Down(context.Background(), name)
			return fmt.Errorf("copying API key: %w", err)
		}

		// Fix permissions (as root)
		chownCmd := []string{"bash", "-c", "chmod 600 /home/claude/.anthropic/api_key"}
		if err := r.SSHAsRoot(ctx, name, chownCmd...); err != nil {
			_ = r.Down(context.Background(), name)
			return fmt.Errorf("fixing API key permissions: %w", err)
		}
	}

	// Configure environment as claude user via SSH (auto-wraps in su)
	if err := r.SSH(ctx, name, "mkdir", "-p", "~/workspace", "~/.config/claude-code"); err != nil {
		_ = r.Down(context.Background(), name)
		return fmt.Errorf("creating workspace dirs: %w", err)
	}
	if err := r.SSH(ctx, name, "bash", "-c", "cd ~/workspace && git init"); err != nil {
		_ = r.Down(context.Background(), name)
		return fmt.Errorf("git init workspace: %w", err)
	}

	return nil
}

// Start starts a stopped container.
func (r *PodmanRunner) Start(ctx context.Context, name string) error {
	return r.run(ctx, "start", name)
}

// SSH runs a command in the container as the claude user.
// Commands are wrapped in "su - claude -c ..." automatically.
func (r *PodmanRunner) SSH(ctx context.Context, name string, command ...string) error {
	args := []string{"exec", name}
	args = append(args, r.wrapUserCommand(command...)...)
	return r.run(ctx, args...)
}

// SSHAsRoot runs a command in the container as root (no su wrapping).
// Used internally during provisioning.
func (r *PodmanRunner) SSHAsRoot(ctx context.Context, name string, command ...string) error {
	args := []string{"exec", name}
	args = append(args, command...)
	return r.run(ctx, args...)
}

// SSHProxy runs an interactive command in the container as the claude user
// with TTY allocation.
func (r *PodmanRunner) SSHProxy(ctx context.Context, name string, opts *SSHOpts, command ...string) error {
	args := []string{"exec", "-it", name}
	args = append(args, r.wrapUserCommand(command...)...)
	return r.runInteractive(ctx, args...)
}

// SSHProxyOutput runs a command in the container as the claude user,
// writing stdout to w.
func (r *PodmanRunner) SSHProxyOutput(ctx context.Context, name string, w io.Writer, opts *SSHOpts, command ...string) error {
	args := []string{"exec", name}
	args = append(args, r.wrapUserCommand(command...)...)
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman exec: %w", err)
	}
	return nil
}

// Pull fetches committed code from the sandbox.
func (r *PodmanRunner) Pull(ctx context.Context, name, remotePath, localRepoDir string) error {
	if err := os.MkdirAll(localRepoDir, 0755); err != nil {
		return fmt.Errorf("creating local repo dir: %w", err)
	}

	if _, err := os.Stat(filepath.Join(localRepoDir, ".git")); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "git", "init")
		cmd.Dir = localRepoDir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}

	tmpDir, err := os.MkdirTemp("", "drella-pull-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cpSrc := ":" + remotePath + "/."
	if err := r.Cp(ctx, name, cpSrc, tmpDir); err != nil {
		return fmt.Errorf("copying from container: %w", err)
	}

	cpCmd := exec.CommandContext(ctx, "cp", "-r", tmpDir+"/.", localRepoDir)
	if err := cpCmd.Run(); err != nil {
		return fmt.Errorf("copying to local repo: %w", err)
	}

	// Stage and commit the copied files so the local repo has git history.
	// Unlike gjoll's Pull (which uses git bundles), podman's Pull is a file copy,
	// so we create a commit to maintain consistent git semantics for callers.
	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = localRepoDir
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	commitCmd := exec.CommandContext(ctx, "git", "commit", "--allow-empty-message", "-m", "Pull from sandbox")
	commitCmd.Dir = localRepoDir
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=orchestrator",
		"GIT_AUTHOR_EMAIL=orchestrator@localhost",
		"GIT_COMMITTER_NAME=orchestrator",
		"GIT_COMMITTER_EMAIL=orchestrator@localhost",
	)
	// Ignore error — commit fails if there are no changes, which is fine
	_ = commitCmd.Run()

	return nil
}

// Cp copies files to/from a container.
// Remote paths use the convention ":path" (e.g. ":~/file" or ":/abs/path").
// PodmanRunner translates these to "name:/path" format for podman cp,
// expanding "~" to "/home/claude".
// When copying TO the container (dest starts with ":"), files are chown'd
// to the claude user automatically since podman cp copies as root.
func (r *PodmanRunner) Cp(ctx context.Context, name, src, dest string) error {
	isRemoteDest := strings.HasPrefix(dest, ":")
	translatedDest := r.translatePath(name, dest)
	if err := r.run(ctx, "cp", r.translatePath(name, src), translatedDest); err != nil {
		return err
	}
	// Fix ownership when copying to the container — podman cp creates
	// files as root, but the claude user needs to own them.
	if isRemoteDest {
		remotePath := dest[1:] // strip leading ":"
		if strings.HasPrefix(remotePath, "~/") {
			remotePath = "/home/claude/" + remotePath[2:]
		} else if remotePath == "~" {
			remotePath = "/home/claude"
		}
		_ = r.SSHAsRoot(ctx, name, "chown", "-R", "claude:claude", remotePath)
	}
	return nil
}

// translatePath converts ":path" sandbox convention to "name:path" podman format.
func (r *PodmanRunner) translatePath(name, path string) string {
	if !strings.HasPrefix(path, ":") {
		return path
	}
	remotePath := path[1:] // strip leading ":"
	// Expand ~ to /home/claude
	if strings.HasPrefix(remotePath, "~/") {
		remotePath = "/home/claude/" + remotePath[2:]
	} else if remotePath == "~" {
		remotePath = "/home/claude"
	}
	return name + ":" + remotePath
}

// Stop stops a running container.
func (r *PodmanRunner) Stop(ctx context.Context, name string) error {
	return r.run(ctx, "stop", name)
}

// Down destroys a container.
func (r *PodmanRunner) Down(ctx context.Context, name string) error {
	return r.run(ctx, "rm", "-f", name)
}

func (r *PodmanRunner) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman %s: %w", args[0], err)
	}
	return nil
}

func (r *PodmanRunner) runInteractive(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman %s: %w", args[0], err)
	}
	return nil
}

// wrapUserCommand wraps a command to run as the claude user via su.
// The command args are joined into a single shell command string.
func (r *PodmanRunner) wrapUserCommand(command ...string) []string {
	// Join all args into a single command string for su -c
	cmdStr := strings.Join(command, " ")
	return []string{"bash", "-c", fmt.Sprintf("su - claude -c %s", shellQuoteForSu(cmdStr))}
}

// shellQuoteForSu wraps a string in single quotes for safe use in su -c.
func shellQuoteForSu(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
