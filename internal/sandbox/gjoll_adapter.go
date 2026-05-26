package sandbox

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/drellabot/orchestrator/internal/gjoll"
	"github.com/drellabot/orchestrator/internal/shellutil"
)

// GjollAdapter wraps gjoll.Runner to implement the sandbox.Runner interface.
type GjollAdapter struct {
	runner *gjoll.Runner
}

// NewGjollAdapter creates a sandbox.Runner from a gjoll.Runner.
func NewGjollAdapter(gjollBin string) *GjollAdapter {
	return &GjollAdapter{
		runner: gjoll.New(gjollBin),
	}
}

// Up provisions a sandbox VM using gjoll.
// config is the path to the .tf file.
func (a *GjollAdapter) Up(ctx context.Context, name string, config string) error {
	return a.runner.Up(ctx, name, config)
}

// Start starts a stopped sandbox.
func (a *GjollAdapter) Start(ctx context.Context, name string) error {
	return a.runner.Start(ctx, name)
}

// SSH runs a command without proxy/tunnels.
func (a *GjollAdapter) SSH(ctx context.Context, name string, command ...string) error {
	return a.runner.SSH(ctx, name, command...)
}

// SSHProxy runs a command with proxy and reverse tunnels.
func (a *GjollAdapter) SSHProxy(ctx context.Context, name string, opts *SSHOpts, command ...string) error {
	return a.runner.SSHProxy(ctx, name, toGjollOpts(opts), command...)
}

// SSHProxyOutput runs a command with proxy, writing stdout to w.
func (a *GjollAdapter) SSHProxyOutput(ctx context.Context, name string, w io.Writer, opts *SSHOpts, command ...string) error {
	return a.runner.SSHProxyOutput(ctx, name, w, toGjollOpts(opts), command...)
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

// HelperScripts returns shell script contents for sandbox-cp and sandbox-ssh.
func (a *GjollAdapter) HelperScripts(name string) (cpScript, sshScript string) {
	quoted := shellutil.Quote(name)
	cpScript = fmt.Sprintf("#!/bin/bash\nset -euo pipefail\ngjoll cp %s \"$1\" \"$2\"\n", quoted)
	sshScript = fmt.Sprintf("#!/bin/bash\nset -euo pipefail\ngjoll ssh %s -- \"$@\"\n", quoted)
	return
}
