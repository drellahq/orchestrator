package github

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner wraps the gh CLI for GitHub operations (fork, push, PR).
type Runner struct {
	bin string // path to gh binary, default "gh"
}

// New creates a Runner. If bin is empty, "gh" is used (found via PATH).
func New(bin string) *Runner {
	if bin == "" {
		bin = "gh"
	}
	return &Runner{bin: bin}
}

// AuthenticatedUser returns the login of the currently authenticated GitHub user.
func (r *Runner) AuthenticatedUser(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "", r.bin, "api", "/user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("getting authenticated user: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// EnsureFork ensures a fork of upstream exists for the authenticated user.
// It returns the full name of the fork (e.g. "user/repo").
func (r *Runner) EnsureFork(ctx context.Context, upstream string) (string, error) {
	out, err := r.run(ctx, "", r.bin, "repo", "fork", upstream, "--clone=false", "--default-branch-only")
	if err != nil {
		return "", fmt.Errorf("forking %s: %w", upstream, err)
	}

	// gh repo fork prints lines like:
	//   "Created fork user/repo" or "user/repo already exists"
	// Extract the owner/repo from the output.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Created fork ") {
			return strings.TrimPrefix(line, "Created fork "), nil
		}
		if strings.Contains(line, " already exists") {
			// "user/repo already exists"
			parts := strings.Fields(line)
			if len(parts) > 0 {
				return parts[0], nil
			}
		}
	}

	// Fallback: if we can't parse, use the authenticated user + repo name
	parts := strings.SplitN(upstream, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("cannot determine fork name from output: %s", out)
	}
	user, err := r.AuthenticatedUser(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot determine fork owner: %w", err)
	}
	return user + "/" + parts[1], nil
}

// PushBranch creates a named branch from sourceRef in repoDir and pushes it
// to the fork via the gh credential helper.
func (r *Runner) PushBranch(ctx context.Context, repoDir, forkFullName, branch, sourceRef string) error {
	return r.pushBranch(ctx, "git", repoDir, forkFullName, branch, sourceRef)
}

func (r *Runner) pushBranch(ctx context.Context, gitBin, repoDir, forkFullName, branch, sourceRef string) error {
	// Create the branch from the source ref
	if _, err := r.run(ctx, repoDir, gitBin, "checkout", "-B", branch, sourceRef); err != nil {
		return fmt.Errorf("creating branch %s: %w", branch, err)
	}

	// Add or update the fork remote
	forkURL := "https://github.com/" + forkFullName + ".git"
	// Try adding first; if it already exists, update the URL
	if _, err := r.run(ctx, repoDir, gitBin, "remote", "add", "fork", forkURL); err != nil {
		if _, err := r.run(ctx, repoDir, gitBin, "remote", "set-url", "fork", forkURL); err != nil {
			return fmt.Errorf("setting fork remote: %w", err)
		}
	}

	// Configure credential helper via gh
	if _, err := r.run(ctx, repoDir, r.bin, "auth", "setup-git"); err != nil {
		return fmt.Errorf("setting up git auth: %w", err)
	}

	// Push to the fork
	if _, err := r.run(ctx, repoDir, gitBin, "push", "--force", "fork", branch); err != nil {
		return fmt.Errorf("pushing branch %s: %w", branch, err)
	}

	return nil
}

// CreatePR opens a pull request on upstream from forkOwner:branch into base.
// It returns the URL of the created PR.
func (r *Runner) CreatePR(ctx context.Context, upstream, forkOwner, branch, base, title, body string) (string, error) {
	head := forkOwner + ":" + branch
	out, err := r.run(ctx, "", r.bin, "pr", "create",
		"--repo", upstream,
		"--head", head,
		"--base", base,
		"--title", title,
		"--body", body,
	)
	if err != nil {
		return "", fmt.Errorf("creating PR: %w", err)
	}

	// gh pr create prints the PR URL on stdout
	return strings.TrimSpace(out), nil
}

func (r *Runner) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w\nstderr: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}
