package gjoll

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Runner wraps gjoll CLI commands. All methods shell out to the gjoll binary.
type Runner struct {
	bin string // path to gjoll binary, default "gjoll"
}

// New creates a Runner. If bin is empty, "gjoll" is used (found via PATH).
func New(bin string) *Runner {
	if bin == "" {
		bin = "gjoll"
	}
	return &Runner{bin: bin}
}

// Up provisions a sandbox VM from a .tf environment.
func (r *Runner) Up(ctx context.Context, name, tfPath string) error {
	return r.run(ctx, nil, "up", "-n", name, tfPath)
}

// Start starts a stopped sandbox VM.
func (r *Runner) Start(ctx context.Context, name string) error {
	return r.run(ctx, nil, "start", name)
}

// SSH runs a command inside the sandbox (no proxy tunnels).
func (r *Runner) SSH(ctx context.Context, name string, command ...string) error {
	args := []string{"ssh", name, "--"}
	args = append(args, command...)
	return r.run(ctx, nil, args...)
}

// SSHProxy runs a command inside the sandbox with proxy tunnels active.
// Stdin/stdout/stderr are connected to the current process.
func (r *Runner) SSHProxy(ctx context.Context, name string, command ...string) error {
	args := []string{"ssh", "--proxy", name, "--"}
	args = append(args, command...)
	return r.runInteractive(ctx, args...)
}

// Pull fetches committed code from the sandbox into a local git repo.
// It initializes the local repo if needed.
func (r *Runner) Pull(ctx context.Context, name, remotePath, localRepoDir string) error {
	// Ensure local dir exists and is a git repo
	if err := os.MkdirAll(localRepoDir, 0755); err != nil {
		return fmt.Errorf("creating local repo dir: %w", err)
	}

	if _, err := os.Stat(localRepoDir + "/.git"); os.IsNotExist(err) {
		if err := r.runInDir(ctx, localRepoDir, nil, "git", "init"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}

	return r.runInDir(ctx, localRepoDir, nil, r.bin, "pull", name, "--path", remotePath)
}

// Cp copies files to/from a sandbox.
func (r *Runner) Cp(ctx context.Context, name, src, dest string) error {
	return r.run(ctx, nil, "cp", name, src, dest)
}

// Stop stops a running sandbox.
func (r *Runner) Stop(ctx context.Context, name string) error {
	return r.run(ctx, nil, "stop", name)
}

// SSHProxyOutput runs a command inside the sandbox with proxy tunnels active,
// writing the command's stdout to w instead of os.Stdout.
func (r *Runner) SSHProxyOutput(ctx context.Context, name string, w io.Writer, command ...string) error {
	args := []string{"ssh", "--proxy", name, "--"}
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gjoll %s: %w", args[0], err)
	}
	return nil
}

// Down destroys a sandbox and all its resources.
func (r *Runner) Down(ctx context.Context, name string) error {
	return r.run(ctx, nil, "down", name)
}

func (r *Runner) run(ctx context.Context, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Stdout = os.Stderr // gjoll output goes to stderr
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gjoll %s: %w", args[0], err)
	}
	return nil
}

func (r *Runner) runInteractive(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gjoll %s: %w", args[0], err)
	}
	return nil
}

func (r *Runner) runInDir(ctx context.Context, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}
