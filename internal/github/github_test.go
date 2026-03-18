package github

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeArgCapture creates a shell script that writes its arguments to a file
// and optionally prints canned stdout. Returns the script path and the args output path.
func writeArgCapture(t *testing.T, stdout string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "capture")
	outFile := filepath.Join(dir, "args.txt")

	content := "#!/bin/sh\necho '---' >> " + outFile + "\nprintf '%s\\n' \"$@\" >> " + outFile + "\nprintf '%s' '" + stdout + "'\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script, outFile
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	invocations := parseInvocations(t, path)
	if len(invocations) == 0 {
		return nil
	}
	return invocations[0]
}

// parseInvocations splits the captured args file into per-invocation arg lists.
func parseInvocations(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading captured args: %v", err)
	}
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return nil
	}

	var invocations [][]string
	var current []string
	for _, line := range strings.Split(s, "\n") {
		if line == "---" {
			if current != nil {
				invocations = append(invocations, current)
			}
			current = []string{}
			continue
		}
		current = append(current, line)
	}
	if current != nil {
		invocations = append(invocations, current)
	}
	return invocations
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		bin     string
		wantBin string
	}{
		{name: "default binary", bin: "", wantBin: "gh"},
		{name: "custom binary", bin: "/usr/local/bin/gh", wantBin: "/usr/local/bin/gh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(tt.bin)
			if r.bin != tt.wantBin {
				t.Errorf("bin = %q, want %q", r.bin, tt.wantBin)
			}
		})
	}
}

func TestAuthenticatedUser(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "testuser\n")
	r := New(script)

	user, err := r.AuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "testuser" {
		t.Errorf("user = %q, want %q", user, "testuser")
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"api", "/user", "--jq", ".login"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("got %d args %v, want %d args %v", len(gotArgs), gotArgs, len(wantArgs), wantArgs)
	}
	for i, want := range wantArgs {
		if gotArgs[i] != want {
			t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], want)
		}
	}
}

func TestEnsureFork(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	tests := []struct {
		name     string
		stdout   string
		upstream string
		wantFork string
		wantArgs []string
	}{
		{
			name:     "new fork created",
			stdout:   "Created fork testuser/osbuild\n",
			upstream: "osbuild/osbuild",
			wantFork: "testuser/osbuild",
			wantArgs: []string{"repo", "fork", "osbuild/osbuild", "--clone=false", "--default-branch-only"},
		},
		{
			name:     "fork already exists",
			stdout:   "testuser/osbuild already exists\n",
			upstream: "osbuild/osbuild",
			wantFork: "testuser/osbuild",
			wantArgs: []string{"repo", "fork", "osbuild/osbuild", "--clone=false", "--default-branch-only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, outFile := writeArgCapture(t, tt.stdout)
			r := New(script)

			fork, err := r.EnsureFork(context.Background(), tt.upstream)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fork != tt.wantFork {
				t.Errorf("fork = %q, want %q", fork, tt.wantFork)
			}

			gotArgs := readArgs(t, outFile)
			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("got %d args %v, want %d args %v", len(gotArgs), gotArgs, len(tt.wantArgs), tt.wantArgs)
			}
			for i, want := range tt.wantArgs {
				if gotArgs[i] != want {
					t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], want)
				}
			}
		})
	}
}

func TestCreatePR(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "https://github.com/osbuild/osbuild/pull/42\n")
	r := New(script)

	url, err := r.CreatePR(context.Background(), "osbuild/osbuild", "testuser", "fix-bug", "main", "Fix bug", "This fixes the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/osbuild/osbuild/pull/42" {
		t.Errorf("url = %q, want PR URL", url)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{
		"pr", "create",
		"--repo", "osbuild/osbuild",
		"--head", "testuser:fix-bug",
		"--base", "main",
		"--title", "Fix bug",
		"--body", "This fixes the bug",
	}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("got %d args %v, want %d args %v", len(gotArgs), gotArgs, len(wantArgs), wantArgs)
	}
	for i, want := range wantArgs {
		if gotArgs[i] != want {
			t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], want)
		}
	}
}

func TestPushBranch(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	// PushBranch runs multiple commands using both git and gh binaries.
	// We capture all invocations with fake scripts.
	gitScript, gitOutFile := writeArgCapture(t, "")
	ghScript, ghOutFile := writeArgCapture(t, "")
	r := New(ghScript)
	repoDir := t.TempDir()

	err := r.pushBranch(context.Background(), gitScript, repoDir, "testuser/osbuild", "fix-bug", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify git commands: checkout, remote add, push
	gitInvocations := parseInvocations(t, gitOutFile)
	if len(gitInvocations) < 3 {
		t.Fatalf("expected at least 3 git invocations, got %d: %v", len(gitInvocations), gitInvocations)
	}

	// First: git checkout -B fix-bug refs/heads/main
	wantCheckout := []string{"checkout", "-B", "fix-bug", "refs/heads/main"}
	if got := gitInvocations[0]; !equalArgs(got, wantCheckout) {
		t.Errorf("git invocation 0 = %v, want %v", got, wantCheckout)
	}

	// Second: git remote add fork <url>
	wantRemote := []string{"remote", "add", "fork", "https://github.com/testuser/osbuild.git"}
	if got := gitInvocations[1]; !equalArgs(got, wantRemote) {
		t.Errorf("git invocation 1 = %v, want %v", got, wantRemote)
	}

	// Third: git push --force fork fix-bug
	wantPush := []string{"push", "--force", "fork", "fix-bug"}
	if got := gitInvocations[2]; !equalArgs(got, wantPush) {
		t.Errorf("git invocation 2 = %v, want %v", got, wantPush)
	}

	// Verify gh was called with auth setup-git
	ghArgs := readArgs(t, ghOutFile)
	if len(ghArgs) < 2 || ghArgs[0] != "auth" || ghArgs[1] != "setup-git" {
		t.Errorf("gh args = %v, want [auth setup-git]", ghArgs)
	}
}

// initGitRepo creates a git repo in dir with an initial commit, returning
// the path. Useful for testing operations that need real git state.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@test.com"},
		{"commit", "--allow-empty", "-m", "Initial commit"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// addCommit creates a commit in the given repo.
func addCommit(t *testing.T, dir, message string) {
	t.Helper()
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", message)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

// gitLog returns commit messages for the given range.
func gitLog(t *testing.T, dir, rangeSpec string) []string {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%B%x00", rangeSpec)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log %s: %v\n%s", rangeSpec, err, out)
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil
	}
	var msgs []string
	for _, msg := range strings.Split(raw, "\x00") {
		msg = strings.TrimSpace(msg)
		if msg != "" {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

func TestAddCoAuthorTrailers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	t.Run("adds trailer to new commits", func(t *testing.T) {
		// Set up an "upstream" repo with a base branch
		upstreamDir := t.TempDir()
		initGitRepo(t, upstreamDir)
		addCommit(t, upstreamDir, "upstream commit 1")
		addCommit(t, upstreamDir, "upstream commit 2")

		// Set up a "local" repo that clones from upstream
		localDir := t.TempDir()
		initGitRepo(t, localDir)

		// Add upstream remote and fetch
		cmd := exec.Command("git", "remote", "add", "upstream", upstreamDir)
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote add: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "fetch", "upstream", "master")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fetch: %v\n%s", err, out)
		}

		// Reset local to upstream/master
		cmd = exec.Command("git", "reset", "--hard", "upstream/master")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("reset: %v\n%s", err, out)
		}

		// Create a source branch with new commits
		cmd = exec.Command("git", "checkout", "-b", "gjoll-test-task")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("checkout: %v\n%s", err, out)
		}
		addCommit(t, localDir, "new commit 1")
		addCommit(t, localDir, "new commit 2")

		// Remove the upstream remote so addCoAuthorTrailers re-adds it
		cmd = exec.Command("git", "remote", "remove", "upstream")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote remove: %v\n%s", err, out)
		}

		r := New("")
		trailer := "Co-authored-by: Test Author <test@example.com>"
		err := r.addCoAuthorTrailers(context.Background(), "git", localDir, upstreamDir, "master", "gjoll-test-task", trailer)

		if err != nil {
			t.Fatalf("addCoAuthorTrailers: %v", err)
		}

		// Verify trailers were added to new commits
		msgs := gitLog(t, localDir, "upstream/master..HEAD")
		if len(msgs) != 2 {
			t.Fatalf("expected 2 new commits, got %d: %v", len(msgs), msgs)
		}
		for i, msg := range msgs {
			if !strings.Contains(msg, trailer) {
				t.Errorf("commit %d missing trailer: %q", i, msg)
			}
		}

		// Verify upstream commits are untouched
		upstreamMsgs := gitLog(t, localDir, "upstream/master")
		for _, msg := range upstreamMsgs {
			if strings.Contains(msg, "Co-authored-by") {
				t.Errorf("upstream commit should not have trailer: %q", msg)
			}
		}
	})

	t.Run("idempotent - does not duplicate trailers", func(t *testing.T) {
		upstreamDir := t.TempDir()
		initGitRepo(t, upstreamDir)

		localDir := t.TempDir()
		initGitRepo(t, localDir)

		cmd := exec.Command("git", "remote", "add", "upstream", upstreamDir)
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote add: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "fetch", "upstream", "master")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fetch: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "reset", "--hard", "upstream/master")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("reset: %v\n%s", err, out)
		}

		cmd = exec.Command("git", "checkout", "-b", "gjoll-test-task")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("checkout: %v\n%s", err, out)
		}

		// Create a commit that already has the trailer
		addCommit(t, localDir, "already traced\n\nCo-authored-by: Test Author <test@example.com>")

		cmd = exec.Command("git", "remote", "remove", "upstream")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote remove: %v\n%s", err, out)
		}

		r := New("")
		trailer := "Co-authored-by: Test Author <test@example.com>"
		err := r.addCoAuthorTrailers(context.Background(), "git", localDir, upstreamDir, "master", "gjoll-test-task", trailer)

		if err != nil {
			t.Fatalf("addCoAuthorTrailers: %v", err)
		}

		msgs := gitLog(t, localDir, "upstream/master..HEAD")
		if len(msgs) != 1 {
			t.Fatalf("expected 1 commit, got %d", len(msgs))
		}
		// Count occurrences of the trailer
		count := strings.Count(msgs[0], "Co-authored-by: Test Author")
		if count != 1 {
			t.Errorf("trailer appears %d times, want 1: %q", count, msgs[0])
		}
	})

	t.Run("no new commits is a no-op", func(t *testing.T) {
		upstreamDir := t.TempDir()
		initGitRepo(t, upstreamDir)

		localDir := t.TempDir()
		initGitRepo(t, localDir)

		cmd := exec.Command("git", "remote", "add", "upstream", upstreamDir)
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote add: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "fetch", "upstream", "master")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fetch: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "reset", "--hard", "upstream/master")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("reset: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "checkout", "-b", "gjoll-test-task")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("checkout: %v\n%s", err, out)
		}

		cmd = exec.Command("git", "remote", "remove", "upstream")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote remove: %v\n%s", err, out)
		}

		r := New("")
		err := r.addCoAuthorTrailers(context.Background(), "git", localDir, upstreamDir, "master", "gjoll-test-task", "Co-authored-by: X <x@x.com>")
		if err != nil {
			t.Fatalf("addCoAuthorTrailers with no new commits: %v", err)
		}
	})
}

func TestCommentOnPR(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "")
	r := New(script)

	err := r.CommentOnPR(context.Background(), "https://github.com/osbuild/osbuild/pull/42", "LGTM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"pr", "comment", "https://github.com/osbuild/osbuild/pull/42", "--body", "LGTM"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("got %d args %v, want %d args %v", len(gotArgs), gotArgs, len(wantArgs), wantArgs)
	}
	for i, want := range wantArgs {
		if gotArgs[i] != want {
			t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], want)
		}
	}
}

func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
