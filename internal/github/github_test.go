package github

import (
	"context"
	"fmt"
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
		"--draft",
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
		{"init", "-b", "main"},
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
		cmd = exec.Command("git", "fetch", "upstream", "main")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fetch: %v\n%s", err, out)
		}

		// Reset local to upstream/main
		cmd = exec.Command("git", "reset", "--hard", "upstream/main")
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
		err := r.addCoAuthorTrailers(context.Background(), "git", localDir, upstreamDir, "main", "gjoll-test-task", trailer)

		if err != nil {
			t.Fatalf("addCoAuthorTrailers: %v", err)
		}

		// Verify trailers were added to new commits
		msgs := gitLog(t, localDir, "upstream/main..HEAD")
		if len(msgs) != 2 {
			t.Fatalf("expected 2 new commits, got %d: %v", len(msgs), msgs)
		}
		for i, msg := range msgs {
			if !strings.Contains(msg, trailer) {
				t.Errorf("commit %d missing trailer: %q", i, msg)
			}
		}

		// Verify upstream commits are untouched
		upstreamMsgs := gitLog(t, localDir, "upstream/main")
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
		cmd = exec.Command("git", "fetch", "upstream", "main")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fetch: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "reset", "--hard", "upstream/main")
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
		err := r.addCoAuthorTrailers(context.Background(), "git", localDir, upstreamDir, "main", "gjoll-test-task", trailer)

		if err != nil {
			t.Fatalf("addCoAuthorTrailers: %v", err)
		}

		msgs := gitLog(t, localDir, "upstream/main..HEAD")
		if len(msgs) != 1 {
			t.Fatalf("expected 1 commit, got %d", len(msgs))
		}
		// Count occurrences of the trailer
		count := strings.Count(msgs[0], "Co-authored-by: Test Author")
		if count != 1 {
			t.Errorf("trailer appears %d times, want 1: %q", count, msgs[0])
		}
	})

	t.Run("shell metacharacters in trailer are not executed", func(t *testing.T) {
		upstreamDir := t.TempDir()
		initGitRepo(t, upstreamDir)

		localDir := t.TempDir()
		initGitRepo(t, localDir)

		cmd := exec.Command("git", "remote", "add", "upstream", upstreamDir)
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote add: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "fetch", "upstream", "main")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fetch: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "reset", "--hard", "upstream/main")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("reset: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "checkout", "-b", "gjoll-test-task")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("checkout: %v\n%s", err, out)
		}
		addCommit(t, localDir, "new commit")

		cmd = exec.Command("git", "remote", "remove", "upstream")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote remove: %v\n%s", err, out)
		}

		// Use a trailer with shell metacharacters that would execute a command
		// if not properly escaped. The $(touch ...) would create a file.
		sentinel := filepath.Join(t.TempDir(), "pwned")
		trailer := fmt.Sprintf(`Co-authored-by: Evil "$(touch %s)" <evil@example.com>`, sentinel)

		r := New("")
		err := r.addCoAuthorTrailers(context.Background(), "git", localDir, upstreamDir, "main", "gjoll-test-task", trailer)
		if err != nil {
			t.Fatalf("addCoAuthorTrailers: %v", err)
		}

		// The sentinel file must NOT exist — if it does, shell injection occurred.
		if _, err := os.Stat(sentinel); err == nil {
			t.Fatal("shell injection: sentinel file was created by trailer containing shell metacharacters")
		}

		// The trailer should appear literally in the commit message
		msgs := gitLog(t, localDir, "upstream/main..HEAD")
		if len(msgs) != 1 {
			t.Fatalf("expected 1 commit, got %d", len(msgs))
		}
		if !strings.Contains(msgs[0], `Co-authored-by: Evil "$(touch`) {
			t.Errorf("commit message should contain the trailer literally, got: %q", msgs[0])
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
		cmd = exec.Command("git", "fetch", "upstream", "main")
		cmd.Dir = localDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fetch: %v\n%s", err, out)
		}
		cmd = exec.Command("git", "reset", "--hard", "upstream/main")
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
		err := r.addCoAuthorTrailers(context.Background(), "git", localDir, upstreamDir, "main", "gjoll-test-task", "Co-authored-by: X <x@x.com>")
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

func TestCommentOnIssue(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "")
	r := New(script)

	err := r.CommentOnIssue(context.Background(), "org/tasks", 42, "Opened PR: https://github.com/org/repo/pull/1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"issue", "comment", "42", "--repo", "org/tasks", "--body", "Opened PR: https://github.com/org/repo/pull/1"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("got %d args %v, want %d args %v", len(gotArgs), gotArgs, len(wantArgs), wantArgs)
	}
	for i, want := range wantArgs {
		if gotArgs[i] != want {
			t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], want)
		}
	}
}

func TestUpdatePRTitle(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "")
	r := New(script)

	err := r.UpdatePRTitle(context.Background(), "https://github.com/osbuild/osbuild/pull/42", "New title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"pr", "edit", "https://github.com/osbuild/osbuild/pull/42", "--title", "New title"}
	if !equalArgs(gotArgs, wantArgs) {
		t.Errorf("got args %v, want %v", gotArgs, wantArgs)
	}
}

func TestCloneRepo(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "")
	r := New(script)

	err := r.CloneRepo(context.Background(), "org/repo", "/tmp/dest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"repo", "clone", "org/repo", "/tmp/dest", "--", "--depth=1"}
	if !equalArgs(gotArgs, wantArgs) {
		t.Errorf("args = %v, want %v", gotArgs, wantArgs)
	}
}

func TestListIssues(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	tests := []struct {
		name      string
		stdout    string
		wantCount int
		wantArgs  []string
	}{
		{
			name:      "empty result",
			stdout:    "[]",
			wantCount: 0,
			wantArgs:  []string{"api", "--paginate", "/repos/org/repo/issues?state=open&per_page=100"},
		},
		{
			name:      "issues only",
			stdout:    `[{"number":1,"title":"Bug report","body":"Something broke"},{"number":2,"title":"Feature request","body":"Add dark mode"}]`,
			wantCount: 2,
			wantArgs:  []string{"api", "--paginate", "/repos/org/repo/issues?state=open&per_page=100"},
		},
		{
			name:      "filters out pull requests",
			stdout:    `[{"number":1,"title":"Bug report","body":"Something broke"},{"number":2,"title":"Fix bug","body":"","pull_request":{"url":"https://api.github.com/repos/org/repo/pulls/2"}}]`,
			wantCount: 1,
			wantArgs:  []string{"api", "--paginate", "/repos/org/repo/issues?state=open&per_page=100"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, outFile := writeArgCapture(t, tt.stdout)
			r := New(script)

			issues, err := r.ListIssues(context.Background(), "org/repo")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(issues) != tt.wantCount {
				t.Errorf("got %d issues, want %d", len(issues), tt.wantCount)
			}

			gotArgs := readArgs(t, outFile)
			if !equalArgs(gotArgs, tt.wantArgs) {
				t.Errorf("args = %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestListIssues_FieldValues(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	stdout := `[{"number":42,"title":"Bug report","body":"Something broke"}]`
	script, _ := writeArgCapture(t, stdout)
	r := New(script)

	issues, err := r.ListIssues(context.Background(), "org/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].Number != 42 {
		t.Errorf("Number = %d, want 42", issues[0].Number)
	}
	if issues[0].Title != "Bug report" {
		t.Errorf("Title = %q, want %q", issues[0].Title, "Bug report")
	}
	if issues[0].Body != "Something broke" {
		t.Errorf("Body = %q, want %q", issues[0].Body, "Something broke")
	}
}

func TestListIssues_ParsesLabels(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	stdout := `[{"number":1,"title":"Build RHEL","body":"Build it","labels":[{"name":"rhel"},{"name":"priority"}]}]`
	script, _ := writeArgCapture(t, stdout)
	r := New(script)

	issues, err := r.ListIssues(context.Background(), "org/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if len(issues[0].Labels) != 2 {
		t.Fatalf("got %d labels, want 2", len(issues[0].Labels))
	}
	if issues[0].Labels[0].Name != "rhel" {
		t.Errorf("Labels[0].Name = %q, want %q", issues[0].Labels[0].Name, "rhel")
	}
	if issues[0].Labels[1].Name != "priority" {
		t.Errorf("Labels[1].Name = %q, want %q", issues[0].Labels[1].Name, "priority")
	}
}

func TestIssueHasLabel(t *testing.T) {
	issue := Issue{
		Labels: []Label{{Name: "rhel"}, {Name: "Priority"}},
	}

	if !issue.HasLabel("rhel") {
		t.Error("expected HasLabel(rhel) = true")
	}
	if !issue.HasLabel("RHEL") {
		t.Error("expected HasLabel(RHEL) = true (case-insensitive)")
	}
	if !issue.HasLabel("priority") {
		t.Error("expected HasLabel(priority) = true (case-insensitive)")
	}
	if issue.HasLabel("missing") {
		t.Error("expected HasLabel(missing) = false")
	}
}

func TestIssueLabelNames(t *testing.T) {
	issue := Issue{
		Labels: []Label{{Name: "rhel"}, {Name: "priority"}},
	}

	names := issue.LabelNames()
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
	if names[0] != "rhel" || names[1] != "priority" {
		t.Errorf("names = %v, want [rhel priority]", names)
	}
}

func TestIssueLabelNames_Empty(t *testing.T) {
	issue := Issue{}
	names := issue.LabelNames()
	if len(names) != 0 {
		t.Errorf("expected empty names, got %v", names)
	}
}

func TestFetchIssue(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	stdout := `{"number":7,"title":"Design task","body":"See https://github.com/user-attachments/files/1/doc.md"}`
	script, outFile := writeArgCapture(t, stdout)
	r := New(script)

	issue, err := r.FetchIssue(context.Background(), "org/tasks", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Number != 7 {
		t.Errorf("Number = %d, want 7", issue.Number)
	}
	if !strings.Contains(issue.Body, "user-attachments") {
		t.Errorf("Body = %q", issue.Body)
	}

	gotArgs := readArgs(t, outFile)
	want := []string{"api", "/repos/org/tasks/issues/7"}
	if !equalArgs(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestFetchIssueBody(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	stdout := `{"number":1,"title":"T","body":"hello"}`
	script, _ := writeArgCapture(t, stdout)
	r := New(script)

	body, err := r.FetchIssueBody(context.Background(), "org/tasks", 1)
	if err != nil {
		t.Fatal(err)
	}
	if body != "hello" {
		t.Errorf("body = %q", body)
	}
}

func TestAuthToken(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "gho_testtoken\n")
	r := New(script)

	tok, err := r.AuthToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "gho_testtoken" {
		t.Errorf("token = %q", tok)
	}
	gotArgs := readArgs(t, outFile)
	if !equalArgs(gotArgs, []string{"auth", "token"}) {
		t.Errorf("args = %v", gotArgs)
	}
}

func TestPostReview(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	tests := []struct {
		name     string
		event    string
		wantFlag string
		wantErr  bool
	}{
		{name: "approve", event: "APPROVE", wantFlag: "--approve"},
		{name: "request changes", event: "REQUEST_CHANGES", wantFlag: "--request-changes"},
		{name: "comment", event: "COMMENT", wantFlag: "--comment"},
		{name: "case insensitive", event: "approve", wantFlag: "--approve"},
		{name: "invalid event", event: "INVALID", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, outFile := writeArgCapture(t, "")
			r := New(script)

			err := r.PostReview(context.Background(), "osbuild/osbuild", 42, tt.event, "Looks good")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "invalid review event") {
					t.Errorf("error = %q, want mention of invalid review event", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			gotArgs := readArgs(t, outFile)
			wantArgs := []string{"pr", "review", "42", "--repo", "osbuild/osbuild", tt.wantFlag, "--body", "Looks good"}
			if !equalArgs(gotArgs, wantArgs) {
				t.Errorf("got args %v, want %v", gotArgs, wantArgs)
			}
		})
	}
}

func TestReactToComment_IssueComment(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "{}")
	r := New(script)

	err := r.ReactToComment(context.Background(), "org/repo", 42, IssueComment, "rocket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"api", "--method", "POST", "/repos/org/repo/issues/comments/42/reactions", "-f", "content=rocket"}
	if !equalArgs(gotArgs, wantArgs) {
		t.Errorf("got args %v, want %v", gotArgs, wantArgs)
	}
}

func TestReactToComment_ReviewComment(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "{}")
	r := New(script)

	err := r.ReactToComment(context.Background(), "org/repo", 99, ReviewComment, "confused")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"api", "--method", "POST", "/repos/org/repo/pulls/comments/99/reactions", "-f", "content=confused"}
	if !equalArgs(gotArgs, wantArgs) {
		t.Errorf("got args %v, want %v", gotArgs, wantArgs)
	}
}

func TestReactToComment_UnknownType(t *testing.T) {
	r := New("echo")

	err := r.ReactToComment(context.Background(), "org/repo", 1, CommentType("unknown"), "rocket")
	if err == nil {
		t.Fatal("expected error for unknown comment type")
	}
	if !strings.Contains(err.Error(), "unknown comment type") {
		t.Errorf("error = %q, want mention of unknown comment type", err.Error())
	}
}

func TestReactToIssue(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script, outFile := writeArgCapture(t, "{}")
	r := New(script)

	err := r.ReactToIssue(context.Background(), "org/tasks", 7, "rocket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgs := readArgs(t, outFile)
	wantArgs := []string{"api", "--method", "POST", "/repos/org/tasks/issues/7/reactions", "-f", "content=rocket"}
	if !equalArgs(gotArgs, wantArgs) {
		t.Errorf("got args %v, want %v", gotArgs, wantArgs)
	}
}

func TestListOrgMembers(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	tests := []struct {
		name       string
		stdout     string
		role       string
		wantLogins []string
		wantRole   string
		wantErr    bool
	}{
		{
			name:       "maintainer returns all members",
			stdout:     "alice\nbob\ncharlie\n",
			role:       "maintainer",
			wantLogins: []string{"alice", "bob", "charlie"},
			wantRole:   "all",
		},
		{
			name:       "owner returns admins only",
			stdout:     "alice\n",
			role:       "owner",
			wantLogins: []string{"alice"},
			wantRole:   "admin",
		},
		{
			name:       "empty org",
			stdout:     "",
			role:       "maintainer",
			wantLogins: nil,
			wantRole:   "all",
		},
		{
			name:    "invalid role",
			stdout:  "",
			role:    "contributor",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, outFile := writeArgCapture(t, tt.stdout)
			r := New(script)

			logins, err := r.ListOrgMembers(context.Background(), "testorg", tt.role)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(logins) != len(tt.wantLogins) {
				t.Fatalf("got %d logins %v, want %d %v", len(logins), logins, len(tt.wantLogins), tt.wantLogins)
			}
			for i, want := range tt.wantLogins {
				if logins[i] != want {
					t.Errorf("login[%d] = %q, want %q", i, logins[i], want)
				}
			}

			if tt.wantRole != "" {
				gotArgs := readArgs(t, outFile)
				wantEndpoint := fmt.Sprintf("/orgs/testorg/members?role=%s&per_page=100", tt.wantRole)
				wantArgs := []string{"api", "--paginate", wantEndpoint, "--jq", ".[].login"}
				if !equalArgs(gotArgs, wantArgs) {
					t.Errorf("args = %v, want %v", gotArgs, wantArgs)
				}
			}
		})
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
