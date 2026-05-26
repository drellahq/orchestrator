package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gh "github.com/drellabot/orchestrator/internal/github"
)

func TestTaskNameFromSpec(t *testing.T) {
	tests := []struct {
		specFile string
		want     string
	}{
		{"add-dark-mode.md", "add-dark-mode"},
		{"fix-bug.md", "fix-bug"},
		{"simple", "simple"},
		{"my-feature.md", "my-feature"},
	}

	for _, tt := range tests {
		t.Run(tt.specFile, func(t *testing.T) {
			got := taskNameFromSpec(tt.specFile)
			if got != tt.want {
				t.Errorf("taskNameFromSpec(%q) = %q, want %q", tt.specFile, got, tt.want)
			}
		})
	}
}

func TestLoadSaveProcessedSpecs(t *testing.T) {
	dir := t.TempDir()

	// Loading from non-existent file returns empty map
	ps, err := loadProcessedSpecs(dir)
	if err != nil {
		t.Fatalf("loadProcessedSpecs() error: %v", err)
	}
	if len(ps.Specs) != 0 {
		t.Errorf("expected empty specs, got %d", len(ps.Specs))
	}

	// Save some specs
	ps.Specs["spec-a.md"] = true
	ps.Specs["spec-b.md"] = true
	if err := saveProcessedSpecs(dir, ps); err != nil {
		t.Fatalf("saveProcessedSpecs() error: %v", err)
	}

	// Reload and verify
	ps2, err := loadProcessedSpecs(dir)
	if err != nil {
		t.Fatalf("loadProcessedSpecs() error: %v", err)
	}
	if !ps2.Specs["spec-a.md"] {
		t.Error("expected spec-a.md to be processed")
	}
	if !ps2.Specs["spec-b.md"] {
		t.Error("expected spec-b.md to be processed")
	}
	if ps2.Specs["spec-c.md"] {
		t.Error("expected spec-c.md to NOT be processed")
	}
}

func TestLoadProcessedSpecs_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, processedSpecsFile), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadProcessedSpecs(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadProcessedSpecs_NullSpecs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, processedSpecsFile), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	ps, err := loadProcessedSpecs(dir)
	if err != nil {
		t.Fatalf("loadProcessedSpecs() error: %v", err)
	}
	if ps.Specs == nil {
		t.Fatal("expected non-nil Specs map")
	}
}

// setupSpecsRepo creates a bare git repo containing the given spec files inside
// an in-progress/ directory, and returns the path to a fake "gh" script that
// delegates "repo clone" to "git clone".
func setupSpecsRepo(t *testing.T, specs map[string]string) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	// Create a regular repo with files, then clone it bare.
	workDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-b", "main")

	// Create in-progress/ with spec files
	inProgress := filepath.Join(workDir, "in-progress")
	if err := os.MkdirAll(inProgress, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range specs {
		if err := os.WriteFile(filepath.Join(inProgress, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	runGit("add", ".")
	runGit("commit", "-m", "add specs")

	// Clone to a bare repo (so git clone works against it)
	bareDir := t.TempDir()
	cmd := exec.Command("git", "clone", "--bare", workDir, bareDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare failed: %v\n%s", err, out)
	}

	// Write a fake gh script that handles "repo clone" via git clone
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "gh")
	scriptContent := fmt.Sprintf(`#!/bin/sh
# fake gh: only handles "repo clone <repo> <dest> -- --depth=1"
if [ "$1" = "repo" ] && [ "$2" = "clone" ]; then
  dest="$4"
  shift 4
  # skip the "--" separator
  shift
  exec git clone "$@" "%s" "$dest"
fi
printf '[]'
`, bareDir)

	if err := os.WriteFile(script, []byte(scriptContent), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestCheckForNewSpecs_NoTasksRepo(t *testing.T) {
	dir := t.TempDir()
	d := New(gh.New("echo"), time.Minute, "", dir, nil)
	// tasksRepo is empty, should be a no-op
	d.checkForNewSpecs(context.Background())
	// No crash, no state file created
	if _, err := os.Stat(filepath.Join(dir, processedSpecsFile)); !os.IsNotExist(err) {
		t.Error("expected no processed specs file when tasksRepo is empty")
	}
}

func TestCheckForNewSpecs_PicksUpNewSpec(t *testing.T) {
	dir := t.TempDir()
	specContent := "# Add dark mode\n\nAdd a dark mode toggle to the app."

	script := setupSpecsRepo(t, map[string]string{
		"add-dark-mode.md": specContent,
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	var capturedTasks []struct{ name, desc string }
	done := make(chan struct{}, 1)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		mu.Lock()
		capturedTasks = append(capturedTasks, struct{ name, desc string }{taskName, description})
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	d.checkForNewSpecs(context.Background())

	// Wait for the goroutine
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for newTaskFunc")
	}

	// Give the goroutine time to clear running state
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(capturedTasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(capturedTasks))
	}
	if capturedTasks[0].name != "add-dark-mode" {
		t.Errorf("task name = %q, want %q", capturedTasks[0].name, "add-dark-mode")
	}
	if capturedTasks[0].desc != specContent {
		t.Errorf("task description = %q, want %q", capturedTasks[0].desc, specContent)
	}

	// Verify spec is marked as processed
	ps, err := loadProcessedSpecs(dir)
	if err != nil {
		t.Fatalf("loadProcessedSpecs() error: %v", err)
	}
	if !ps.Specs["add-dark-mode.md"] {
		t.Error("expected add-dark-mode.md to be marked as processed")
	}
}

func TestCheckForNewSpecs_SkipsAlreadyProcessed(t *testing.T) {
	dir := t.TempDir()

	// Pre-mark the spec as processed
	ps := &ProcessedSpecs{Specs: map[string]bool{"add-dark-mode.md": true}}
	if err := saveProcessedSpecs(dir, ps); err != nil {
		t.Fatal(err)
	}

	script := setupSpecsRepo(t, map[string]string{
		"add-dark-mode.md": "content",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	d.checkForNewSpecs(context.Background())

	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called for already processed spec")
	}
}

func TestCheckForNewSpecs_SkipsRunningTask(t *testing.T) {
	dir := t.TempDir()

	script := setupSpecsRepo(t, map[string]string{
		"add-dark-mode.md": "content",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")
	d.SetTaskRunning("add-dark-mode", true)

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	d.checkForNewSpecs(context.Background())

	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called for already running task")
	}

	// Spec should not be marked as processed (so it's retried next cycle)
	ps, err := loadProcessedSpecs(dir)
	if err != nil {
		t.Fatalf("loadProcessedSpecs() error: %v", err)
	}
	if ps.Specs["add-dark-mode.md"] {
		t.Error("spec should not be marked as processed when task is already running")
	}
}

func TestCheckForNewSpecs_SkipsNonMdFiles(t *testing.T) {
	dir := t.TempDir()

	script := setupSpecsRepo(t, map[string]string{
		"README.txt": "not a spec",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	d.checkForNewSpecs(context.Background())

	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called for non-.md file")
	}
}

func TestCheckForNewSpecs_MultipleSpecs(t *testing.T) {
	dir := t.TempDir()

	script := setupSpecsRepo(t, map[string]string{
		"feature-a.md": "Feature A description",
		"feature-b.md": "Feature B description",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	var capturedNames []string
	done := make(chan struct{}, 2)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		mu.Lock()
		capturedNames = append(capturedNames, taskName)
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	d.checkForNewSpecs(context.Background())

	// Wait for both goroutines
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for newTaskFunc (got %d of 2)", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(capturedNames) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(capturedNames))
	}

	// Both should be processed
	ps, err := loadProcessedSpecs(dir)
	if err != nil {
		t.Fatalf("loadProcessedSpecs() error: %v", err)
	}
	if !ps.Specs["feature-a.md"] || !ps.Specs["feature-b.md"] {
		t.Error("expected both specs to be marked as processed")
	}
}

func TestCheckForNewSpecs_SetsRunningState(t *testing.T) {
	dir := t.TempDir()

	script := setupSpecsRepo(t, map[string]string{
		"my-task.md": "content",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	started := make(chan struct{})
	finish := make(chan struct{})
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		close(started)
		<-finish
		return nil
	})

	d.checkForNewSpecs(context.Background())

	// Wait for task to start
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for task to start")
	}

	// Task should be marked as running
	if !d.IsTaskRunning("my-task") {
		t.Error("expected task to be marked as running")
	}

	// Let the task finish
	close(finish)
	time.Sleep(50 * time.Millisecond)

	// Task should be cleared from running
	if d.IsTaskRunning("my-task") {
		t.Error("expected task to no longer be running")
	}
}

func TestCheckForNewSpecs_CloneFails(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	// Use a script that will fail (simulating clone failure)
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	// Should not panic or crash on clone failure
	d.checkForNewSpecs(context.Background())

	if called {
		t.Error("newTaskFunc should not have been called when clone fails")
	}
}

func TestCheckForNewSpecs_IdempotentAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	specContent := "Spec content"

	script := setupSpecsRepo(t, map[string]string{
		"my-feature.md": specContent,
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	callCount := 0
	done := make(chan struct{}, 2)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		mu.Lock()
		callCount++
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	// First call should spawn
	d.checkForNewSpecs(context.Background())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
	time.Sleep(50 * time.Millisecond)

	// Second call should NOT spawn (already processed)
	d.checkForNewSpecs(context.Background())
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("expected newTaskFunc to be called once, got %d", callCount)
	}
}

// --- Issue intake tests ---

// writeIssuesScript creates a fake gh that returns canned JSON for the issues API
// and handles reaction POST calls.
func writeIssuesScript(t *testing.T, issuesJSON string) string {
	script, _ := writeIssuesScriptWithCapture(t, issuesJSON)
	return script
}

// writeIssuesScriptWithCapture creates a fake gh that returns canned JSON for
// the issues API, handles reaction POST calls, and captures reactions to a file.
func writeIssuesScriptWithCapture(t *testing.T, issuesJSON string) (string, string) {
	t.Helper()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	reactionsFile := filepath.Join(dir, "reactions.txt")

	content := fmt.Sprintf(`#!/bin/sh
# Handle reaction API calls (POST method)
for arg in "$@"; do
  if [ "$arg" = "--method" ]; then
    printf '%%s\n' "$*" >> %s
    printf '{}'
    exit 0
  fi
done
printf '%%s' '%s'
`, reactionsFile, issuesJSON)

	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script, reactionsFile
}

func makeIssuesJSON(t *testing.T, issues []gh.Issue) string {
	t.Helper()
	data, err := json.Marshal(issues)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTaskNameFromIssue(t *testing.T) {
	tests := []struct {
		repo   string
		number int
		title  string
		want   string
	}{
		{"org/tasks", 42, "Add dark mode", "tasks-42-add_dark_mode"},
		{"org/my-repo", 1, "Fix the login bug", "my-repo-1-fix_the_login_bug"},
		{"simple", 10, "Hello World", "simple-10-hello_world"},
		{"org/tasks", 7, "Feature: add SSO support!", "tasks-7-feature_add_sso_support"},
		{"org/tasks", 3, "  extra   spaces  ", "tasks-3-extra_spaces"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := taskNameFromIssue(tt.repo, tt.number, tt.title)
			if got != tt.want {
				t.Errorf("taskNameFromIssue(%q, %d, %q) = %q, want %q", tt.repo, tt.number, tt.title, got, tt.want)
			}
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Add dark mode", "add_dark_mode"},
		{"Fix the login bug", "fix_the_login_bug"},
		{"Hello World!", "hello_world"},
		{"  extra   spaces  ", "extra_spaces"},
		{"Feature: add SSO support!", "feature_add_sso_support"},
		{"camelCase", "camelcase"},
		{"with-hyphens", "with_hyphens"},
		{"with_underscores", "with_underscores"},
		{"123 numbers", "123_numbers"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toSnakeCase(tt.input)
			if got != tt.want {
				t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadSaveProcessedIssues(t *testing.T) {
	dir := t.TempDir()

	// Loading from non-existent file returns empty map
	pi, err := loadProcessedIssues(dir)
	if err != nil {
		t.Fatalf("loadProcessedIssues() error: %v", err)
	}
	if len(pi.Issues) != 0 {
		t.Errorf("expected empty issues, got %d", len(pi.Issues))
	}

	// Save some issues
	pi.Issues[1] = true
	pi.Issues[42] = true
	if err := saveProcessedIssues(dir, pi); err != nil {
		t.Fatalf("saveProcessedIssues() error: %v", err)
	}

	// Reload and verify
	pi2, err := loadProcessedIssues(dir)
	if err != nil {
		t.Fatalf("loadProcessedIssues() error: %v", err)
	}
	if !pi2.Issues[1] {
		t.Error("expected issue 1 to be processed")
	}
	if !pi2.Issues[42] {
		t.Error("expected issue 42 to be processed")
	}
	if pi2.Issues[99] {
		t.Error("expected issue 99 to NOT be processed")
	}
}

func TestLoadProcessedIssues_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, processedIssuesFile), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadProcessedIssues(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadProcessedIssues_NullIssues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, processedIssuesFile), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	pi, err := loadProcessedIssues(dir)
	if err != nil {
		t.Fatalf("loadProcessedIssues() error: %v", err)
	}
	if pi.Issues == nil {
		t.Fatal("expected non-nil Issues map")
	}
}

func TestCheckForNewIssues_NoTasksRepo(t *testing.T) {
	dir := t.TempDir()
	d := New(gh.New("echo"), time.Minute, "", dir, nil)
	// tasksRepo is empty, should be a no-op
	d.checkForNewIssues(context.Background())
	// No crash, no state file created
	if _, err := os.Stat(filepath.Join(dir, processedIssuesFile)); !os.IsNotExist(err) {
		t.Error("expected no processed issues file when tasksRepo is empty")
	}
}

func TestCheckForNewIssues_PicksUpNewIssue(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 7, Title: "Add dark mode", Body: "Please add a dark mode toggle.", User: gh.CommentUser{Login: "alice"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	var capturedTasks []struct {
		name, desc, sourceRepo string
		sourceIssue          int
	}
	done := make(chan struct{}, 1)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		mu.Lock()
		capturedTasks = append(capturedTasks, struct {
			name, desc, sourceRepo string
			sourceIssue          int
		}{taskName, description, sourceRepo, sourceIssue})
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	d.checkForNewIssues(context.Background())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for newTaskFunc")
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(capturedTasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(capturedTasks))
	}
	if capturedTasks[0].name != "tasks-7-add_dark_mode" {
		t.Errorf("task name = %q, want %q", capturedTasks[0].name, "tasks-7-add_dark_mode")
	}
	wantDesc := "Add dark mode\n\nPlease add a dark mode toggle."
	if capturedTasks[0].desc != wantDesc {
		t.Errorf("task description = %q, want %q", capturedTasks[0].desc, wantDesc)
	}
	if capturedTasks[0].sourceRepo != "org/tasks" {
		t.Errorf("sourceRepo = %q, want org/tasks", capturedTasks[0].sourceRepo)
	}
	if capturedTasks[0].sourceIssue != 7 {
		t.Errorf("sourceIssue = %d, want 7", capturedTasks[0].sourceIssue)
	}

	// Verify issue is marked as processed
	pi, err := loadProcessedIssues(dir)
	if err != nil {
		t.Fatalf("loadProcessedIssues() error: %v", err)
	}
	if !pi.Issues[7] {
		t.Error("expected issue 7 to be marked as processed")
	}
}

func TestCheckForNewIssues_TitleOnlyWhenNoBody(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 3, Title: "Fix the login bug", Body: "", User: gh.CommentUser{Login: "alice"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	var capturedDesc string
	done := make(chan struct{}, 1)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		capturedDesc = description
		done <- struct{}{}
		return nil
	})

	d.checkForNewIssues(context.Background())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	if capturedDesc != "Fix the login bug" {
		t.Errorf("description = %q, want %q", capturedDesc, "Fix the login bug")
	}
}

func TestCheckForNewIssues_SkipsUnallowedAuthor(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 7, Title: "Add dark mode", Body: "content", User: gh.CommentUser{Login: "stranger"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice", "bob"})
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	d.checkForNewIssues(context.Background())
	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called for issue from unallowed author")
	}

	// Issue should NOT be marked as processed so it can be picked up if the
	// user is later added to the allowlist.
	pi, err := loadProcessedIssues(dir)
	if err != nil {
		t.Fatalf("loadProcessedIssues() error: %v", err)
	}
	if pi.Issues[7] {
		t.Error("issue should not be marked as processed when author is not allowed")
	}
}

func TestCheckForNewIssues_SkipsAlreadyProcessed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	// Pre-mark issue as processed
	pi := &ProcessedIssues{Issues: map[int]bool{7: true}}
	if err := saveProcessedIssues(dir, pi); err != nil {
		t.Fatal(err)
	}

	issues := []gh.Issue{
		{Number: 7, Title: "Add dark mode", Body: "content", User: gh.CommentUser{Login: "alice"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	d.checkForNewIssues(context.Background())
	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called for already processed issue")
	}
}

func TestCheckForNewIssues_SkipsRunningTask(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 7, Title: "Add dark mode", Body: "content", User: gh.CommentUser{Login: "alice"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")
	d.SetTaskRunning("tasks-7-add_dark_mode", true)

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	d.checkForNewIssues(context.Background())
	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called for already running task")
	}

	// Issue should not be marked as processed (so it's retried next cycle)
	pi, err := loadProcessedIssues(dir)
	if err != nil {
		t.Fatalf("loadProcessedIssues() error: %v", err)
	}
	if pi.Issues[7] {
		t.Error("issue should not be marked as processed when task is already running")
	}
}

func TestCheckForNewIssues_FiltersPullRequests(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	// Simulate the raw GitHub API response which includes PRs with pull_request field
	rawJSON := `[{"number":1,"title":"Real issue","body":"Fix this","user":{"login":"alice"}},{"number":2,"title":"A PR","body":"","user":{"login":"alice"},"pull_request":{"url":"https://api.github.com/repos/org/tasks/pulls/2"}}]`
	script := writeIssuesScript(t, rawJSON)

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	var capturedNames []string
	done := make(chan struct{}, 2)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		mu.Lock()
		capturedNames = append(capturedNames, taskName)
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	d.checkForNewIssues(context.Background())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(capturedNames) != 1 {
		t.Fatalf("expected 1 task (PR should be filtered), got %d: %v", len(capturedNames), capturedNames)
	}
	if capturedNames[0] != "tasks-1-real_issue" {
		t.Errorf("task name = %q, want %q", capturedNames[0], "tasks-1-real_issue")
	}
}

func TestCheckForNewIssues_MultipleIssues(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 1, Title: "Issue A", Body: "Body A", User: gh.CommentUser{Login: "alice"}},
		{Number: 2, Title: "Issue B", Body: "Body B", User: gh.CommentUser{Login: "alice"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	var capturedNames []string
	done := make(chan struct{}, 2)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		mu.Lock()
		capturedNames = append(capturedNames, taskName)
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	d.checkForNewIssues(context.Background())

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for newTaskFunc (got %d of 2)", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(capturedNames) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(capturedNames))
	}

	// Both should be processed
	pi, err := loadProcessedIssues(dir)
	if err != nil {
		t.Fatalf("loadProcessedIssues() error: %v", err)
	}
	if !pi.Issues[1] || !pi.Issues[2] {
		t.Error("expected both issues to be marked as processed")
	}
}

func TestCheckForNewIssues_IdempotentAcrossCalls(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 42, Title: "Feature", Body: "Description", User: gh.CommentUser{Login: "alice"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	callCount := 0
	done := make(chan struct{}, 2)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		mu.Lock()
		callCount++
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	// First call should spawn
	d.checkForNewIssues(context.Background())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
	time.Sleep(50 * time.Millisecond)

	// Second call should NOT spawn (already processed)
	d.checkForNewIssues(context.Background())
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("expected newTaskFunc to be called once, got %d", callCount)
	}
}

func TestCheckForNewIssues_SetsRunningState(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 5, Title: "My task", Body: "content", User: gh.CommentUser{Login: "alice"}},
	}
	script := writeIssuesScript(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	started := make(chan struct{})
	finish := make(chan struct{})
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		close(started)
		<-finish
		return nil
	})

	d.checkForNewIssues(context.Background())

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for task to start")
	}

	if !d.IsTaskRunning("tasks-5-my_task") {
		t.Error("expected task to be marked as running")
	}

	close(finish)
	time.Sleep(50 * time.Millisecond)

	if d.IsTaskRunning("tasks-5-my_task") {
		t.Error("expected task to no longer be running")
	}
}

func TestCheckForNewIssues_ReactsRocketOnAccepted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 7, Title: "Add dark mode", Body: "Please add a dark mode toggle.", User: gh.CommentUser{Login: "alice"}},
	}
	script, reactionsFile := writeIssuesScriptWithCapture(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice"})
	d.SetTasksRepo("org/tasks")

	done := make(chan struct{}, 1)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		done <- struct{}{}
		return nil
	})

	d.checkForNewIssues(context.Background())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for newTaskFunc")
	}
	time.Sleep(50 * time.Millisecond)

	reactions := parseIssueReactions(t, reactionsFile)
	foundRocket := false
	for _, r := range reactions {
		if strings.Contains(r, "content=rocket") && strings.Contains(r, "issues/7/reactions") {
			foundRocket = true
			break
		}
	}
	if !foundRocket {
		t.Errorf("expected rocket reaction on issue 7, got: %v", reactions)
	}
}

func TestCheckForNewIssues_ReactsConfusedOnRejected(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	issues := []gh.Issue{
		{Number: 7, Title: "Add dark mode", Body: "content", User: gh.CommentUser{Login: "stranger"}},
	}
	script, reactionsFile := writeIssuesScriptWithCapture(t, makeIssuesJSON(t, issues))

	d := New(gh.New(script), time.Minute, "", dir, []string{"alice", "bob"})
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		called = true
		return nil
	})

	d.checkForNewIssues(context.Background())
	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called for issue from unallowed author")
	}

	reactions := parseIssueReactions(t, reactionsFile)
	foundConfused := false
	for _, r := range reactions {
		if strings.Contains(r, "content=confused") && strings.Contains(r, "issues/7/reactions") {
			foundConfused = true
			break
		}
	}
	if !foundConfused {
		t.Errorf("expected confused reaction on issue 7, got: %v", reactions)
	}
}

func parseIssueReactions(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading reactions file: %v", err)
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
