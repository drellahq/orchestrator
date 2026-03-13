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

	// First: git checkout -B fix-bug main
	wantCheckout := []string{"checkout", "-B", "fix-bug", "main"}
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
