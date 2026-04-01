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

// AddCoAuthorTrailers fetches the upstream base branch, identifies new commits
// on sourceRef, and appends the given trailer to each commit that lacks it.
func (r *Runner) AddCoAuthorTrailers(ctx context.Context, repoDir, upstream, base, sourceRef, trailer string) error {
	upstreamURL := "https://github.com/" + upstream + ".git"
	return r.addCoAuthorTrailers(ctx, "git", repoDir, upstreamURL, base, sourceRef, trailer)
}

func (r *Runner) addCoAuthorTrailers(ctx context.Context, gitBin, repoDir, upstreamURL, base, sourceRef, trailer string) error {
	// Use fully-qualified ref to avoid ambiguity with a remote of the same name.
	qualifiedRef := "refs/heads/" + sourceRef

	// Add upstream remote (or update URL if it exists)
	if _, err := r.run(ctx, repoDir, gitBin, "remote", "add", "upstream", upstreamURL); err != nil {
		if _, err := r.run(ctx, repoDir, gitBin, "remote", "set-url", "upstream", upstreamURL); err != nil {
			return fmt.Errorf("setting upstream remote: %w", err)
		}
	}

	// Fetch the base branch from upstream
	if _, err := r.run(ctx, repoDir, gitBin, "fetch", "upstream", base); err != nil {
		return fmt.Errorf("fetching upstream %s: %w", base, err)
	}

	// Check if there are any new commits
	out, err := r.run(ctx, repoDir, gitBin, "rev-list", "--count", "upstream/"+base+".."+qualifiedRef)
	if err != nil {
		return fmt.Errorf("counting new commits: %w", err)
	}
	if strings.TrimSpace(out) == "0" {
		return nil
	}

	// Checkout the sourceRef so filter-branch can rewrite it.
	// Use -B with the qualified ref to stay on the branch (not detached).
	if _, err := r.run(ctx, repoDir, gitBin, "checkout", "-B", sourceRef, qualifiedRef); err != nil {
		return fmt.Errorf("checking out %s: %w", sourceRef, err)
	}

	// Use git filter-branch to add the trailer to commits that lack it.
	// git interpret-trailers --if-exists doNothing skips the trailer if
	// the commit message already contains one with the same key and value.
	msgFilter := fmt.Sprintf(`git interpret-trailers --trailer "%s" --if-exists doNothing`, trailer)
	cmd := exec.CommandContext(ctx, gitBin, "filter-branch", "-f", "--msg-filter", msgFilter, "upstream/"+base+"..HEAD")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "FILTER_BRANCH_SQUELCH_WARNING=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("filter-branch: %w\nstderr: %s", err, stderr.String())
	}

	return nil
}

// PushBranch creates a named branch from sourceRef in repoDir and pushes it
// to the fork via the gh credential helper.
func (r *Runner) PushBranch(ctx context.Context, repoDir, forkFullName, branch, sourceRef string) error {
	return r.pushBranch(ctx, "git", repoDir, forkFullName, branch, sourceRef)
}

func (r *Runner) pushBranch(ctx context.Context, gitBin, repoDir, forkFullName, branch, sourceRef string) error {
	// Use fully-qualified ref to avoid ambiguity with a remote of the same name.
	qualifiedRef := "refs/heads/" + sourceRef
	if _, err := r.run(ctx, repoDir, gitBin, "checkout", "-B", branch, qualifiedRef); err != nil {
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
		"--draft",
	)
	if err != nil {
		return "", fmt.Errorf("creating PR: %w", err)
	}

	// gh pr create prints the PR URL on stdout
	return strings.TrimSpace(out), nil
}

// CommentOnPR posts a comment on a pull request identified by its URL.
func (r *Runner) CommentOnPR(ctx context.Context, prURL, body string) error {
	_, err := r.run(ctx, "", r.bin, "pr", "comment", prURL, "--body", body)
	if err != nil {
		return fmt.Errorf("commenting on PR: %w", err)
	}
	return nil
}

// UpdatePRTitle changes the title of a pull request identified by its URL.
func (r *Runner) UpdatePRTitle(ctx context.Context, prURL, title string) error {
	_, err := r.run(ctx, "", r.bin, "pr", "edit", prURL, "--title", title)
	if err != nil {
		return fmt.Errorf("updating PR title: %w", err)
	}
	return nil
}

// MarkPRReady converts a draft PR to ready-for-review.
func (r *Runner) MarkPRReady(ctx context.Context, prURL string) error {
	_, err := r.run(ctx, "", r.bin, "pr", "ready", prURL)
	if err != nil {
		return fmt.Errorf("marking PR ready: %w", err)
	}
	return nil
}

// AddLabelToPR adds a label to a pull request.
func (r *Runner) AddLabelToPR(ctx context.Context, prURL, label string) error {
	_, err := r.run(ctx, "", r.bin, "pr", "edit", prURL, "--add-label", label)
	if err != nil {
		return fmt.Errorf("adding label to PR: %w", err)
	}
	return nil
}

// ListRepoFiles lists files in a directory of a repo on a given branch.
// It returns a slice of file paths relative to the directory.
func (r *Runner) ListRepoFiles(ctx context.Context, repo, branch, dir string) ([]string, error) {
	endpoint := fmt.Sprintf("/repos/%s/contents/%s?ref=%s", repo, dir, branch)
	out, err := r.run(ctx, "", r.bin, "api", "--paginate", endpoint, "--jq", ".[].name")
	if err != nil {
		return nil, fmt.Errorf("listing repo files: %w", err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// GetFileContent fetches the raw content of a file from a repo.
func (r *Runner) GetFileContent(ctx context.Context, repo, branch, path string) (string, error) {
	endpoint := fmt.Sprintf("/repos/%s/contents/%s?ref=%s", repo, path, branch)
	out, err := r.run(ctx, "", r.bin, "api", endpoint, "--jq", ".content")
	if err != nil {
		return "", fmt.Errorf("getting file content: %w", err)
	}
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
