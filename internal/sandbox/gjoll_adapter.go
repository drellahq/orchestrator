package sandbox

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/drellahq/orchestrator/internal/gjoll"
	"github.com/drellahq/orchestrator/internal/shellutil"
)

// GjollAdapter wraps gjoll.Runner to implement the sandbox.Runner interface.
type GjollAdapter struct {
	runner *gjoll.Runner
	rhel   *RHELRegistration
}

// NewGjollAdapter creates a sandbox.Runner from a gjoll.Runner.
func NewGjollAdapter(gjollBin string, rhel *RHELRegistration) *GjollAdapter {
	return &GjollAdapter{
		runner: gjoll.New(gjollBin),
		rhel:   rhel,
	}
}

// Up provisions a sandbox VM using gjoll.
// config is the path to the .tf file.
func (a *GjollAdapter) Up(ctx context.Context, name string, config string) error {
	if err := a.runner.Up(ctx, name, config); err != nil {
		return err
	}
	if err := registerRHEL(ctx, a.SSH, name, a.rhel); err != nil {
		_ = a.runner.Down(context.Background(), name)
		return fmt.Errorf("RHEL registration: %w", err)
	}
	return nil
}

// Start starts a stopped sandbox.
func (a *GjollAdapter) Start(ctx context.Context, name string) error {
	return a.runner.Start(ctx, name)
}

// SSH runs a command without proxy/tunnels.
// When callers pass "bash", "-c", <cmd>, the adapter collapses the command
// into a single string because SSH concatenates arguments with spaces and
// the remote shell re-splits them, breaking multi-word bash -c commands.
func (a *GjollAdapter) SSH(ctx context.Context, name string, command ...string) error {
	return a.runner.SSH(ctx, name, collapseBashC(command)...)
}

// SSHProxy runs a command with proxy and reverse tunnels.
func (a *GjollAdapter) SSHProxy(ctx context.Context, name string, opts *SSHOpts, command ...string) error {
	return a.runner.SSHProxy(ctx, name, toGjollOpts(opts), collapseBashC(command)...)
}

// SSHProxyOutput runs a command with proxy, writing stdout to w.
func (a *GjollAdapter) SSHProxyOutput(ctx context.Context, name string, w io.Writer, opts *SSHOpts, command ...string) error {
	return a.runner.SSHProxyOutput(ctx, name, w, toGjollOpts(opts), collapseBashC(command)...)
}

// collapseBashC detects the "bash", "-c", <cmd> pattern and collapses it
// into a single string. SSH transports concatenate arguments with spaces
// before the remote shell re-parses them, which breaks multi-word bash -c
// commands. Collapsing lets the remote sshd's shell interpret the command
// string directly (sshd always runs commands through the user's login shell).
func collapseBashC(command []string) []string {
	if len(command) >= 3 && command[0] == "bash" && command[1] == "-c" {
		return []string{strings.Join(command[2:], " ")}
	}
	return command
}

// toGjollOpts converts sandbox.SSHOpts to gjoll.SSHOpts, handling nil safely.
func toGjollOpts(opts *SSHOpts) *gjoll.SSHOpts {
	if opts == nil {
		return nil
	}
	return &gjoll.SSHOpts{
		Proxy:          opts.Proxy,
		ReverseTunnels: opts.ReverseTunnels,
	}
}

// Pull fetches committed code from the sandbox.
func (a *GjollAdapter) Pull(ctx context.Context, name, remotePath, localRepoDir string) error {
	return a.runner.Pull(ctx, name, remotePath, localRepoDir)
}

// Cp copies files to/from a sandbox.
// Callers use "name:/path" format for remote paths (podman convention).
// This adapter translates to gjoll's ":/path" format.
func (a *GjollAdapter) Cp(ctx context.Context, name, src, dest string) error {
	src = toGjollPath(name, src)
	dest = toGjollPath(name, dest)
	return a.runner.Cp(ctx, name, src, dest)
}

// toGjollPath converts "name:/path" to ":/path" for gjoll.
func toGjollPath(name, p string) string {
	prefix := name + ":"
	if strings.HasPrefix(p, prefix) {
		return ":" + p[len(prefix):]
	}
	return p
}

// Stop stops a running sandbox.
func (a *GjollAdapter) Stop(ctx context.Context, name string) error {
	return a.runner.Stop(ctx, name)
}

// Down destroys a sandbox.
func (a *GjollAdapter) Down(ctx context.Context, name string) error {
	return a.runner.Down(ctx, name)
}

// UserHome returns the SSH user's home directory.
func (a *GjollAdapter) UserHome() string {
	return "~"
}

// AsUser returns the command unchanged since SSH connects as the target user.
func (a *GjollAdapter) AsUser(cmd string) string {
	return cmd
}

// HelperScripts returns shell script contents for sandbox-cp and sandbox-ssh.
func (a *GjollAdapter) HelperScripts(name string) (cpScript, sshScript string) {
	quoted := shellutil.Quote(name)
	cpScript = fmt.Sprintf("#!/bin/bash\nset -euo pipefail\ngjoll cp %s \"$1\" \"$2\"\n", quoted)
	sshScript = fmt.Sprintf("#!/bin/bash\nset -euo pipefail\ngjoll ssh %s -- \"$@\"\n", quoted)
	return
}
