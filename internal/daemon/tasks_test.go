package daemon

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// writeTasksRepoScript creates a fake gh that simulates the GitHub API
// for listing files in in-progress/ and fetching file content.
// files maps filename to content. If files is nil, the listing returns empty.
func writeTasksRepoScript(t *testing.T, files map[string]string) string {
	t.Helper()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "gh")

	// Build the name listing: one name per line
	var nameLines string
	for name := range files {
		nameLines += name + "\n"
	}

	// Build content lookup cases: match on the endpoint argument
	var contentCases string
	for name, content := range files {
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		contentCases += fmt.Sprintf("    *contents/in-progress/%s*) printf '%%s' '%s' ;;\n", name, encoded)
	}

	scriptContent := fmt.Sprintf(`#!/bin/sh
# Determine call type from --jq argument
jq_arg=""
for arg in "$@"; do
  case "$prev" in
    --jq) jq_arg="$arg" ;;
  esac
  prev="$arg"
done

case "$jq_arg" in
  ".[].name")
    printf '%%s' '%s'
    exit 0
    ;;
  ".content")
    all="$*"
    case "$all" in
%s    *) printf 'not found'; exit 1 ;;
    esac
    exit 0
    ;;
esac

printf '[]'
`, nameLines, contentCases)

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
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	specContent := "# Add dark mode\n\nAdd a dark mode toggle to the app."

	script := writeTasksRepoScript(t, map[string]string{
		"add-dark-mode.md": specContent,
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	var capturedTasks []struct{ name, desc string }
	done := make(chan struct{}, 1)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
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
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	// Pre-mark the spec as processed
	ps := &ProcessedSpecs{Specs: map[string]bool{"add-dark-mode.md": true}}
	if err := saveProcessedSpecs(dir, ps); err != nil {
		t.Fatal(err)
	}

	script := writeTasksRepoScript(t, map[string]string{
		"add-dark-mode.md": "content",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
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
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	script := writeTasksRepoScript(t, map[string]string{
		"add-dark-mode.md": "content",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")
	d.SetTaskRunning("add-dark-mode", true)

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
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
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	script := writeTasksRepoScript(t, map[string]string{
		"README.txt": "not a spec",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
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
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	script := writeTasksRepoScript(t, map[string]string{
		"feature-a.md": "Feature A description",
		"feature-b.md": "Feature B description",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	var capturedNames []string
	done := make(chan struct{}, 2)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
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
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	script := writeTasksRepoScript(t, map[string]string{
		"my-task.md": "content",
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	started := make(chan struct{})
	finish := make(chan struct{})
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
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

func TestCheckForNewSpecs_ContextCancelled(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	// Use a script that will fail (simulating cancelled context / API error)
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
		called = true
		return nil
	})

	// Should not panic or crash on API error
	d.checkForNewSpecs(context.Background())

	if called {
		t.Error("newTaskFunc should not have been called when listing fails")
	}
}

func TestCheckForNewSpecs_ErrorFetchingContent(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	// Script that lists files but fails on content fetch
	scriptDir := t.TempDir()
	countFile := filepath.Join(scriptDir, "count")
	script := filepath.Join(scriptDir, "gh")

	content := fmt.Sprintf(`#!/bin/sh
if [ -f %s ]; then
  n=$(cat %s)
else
  n=0
fi
echo $((n + 1)) > %s
case $n in
  0) printf 'broken-spec.md' ;;
  *) exit 1 ;;
esac
`, countFile, countFile, countFile)

	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	called := false
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
		called = true
		return nil
	})

	// Should handle gracefully - no crash
	d.checkForNewSpecs(context.Background())

	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("newTaskFunc should not have been called when content fetch fails")
	}
}

func TestCheckForNewSpecs_IdempotentAcrossCalls(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	specContent := "Spec content"

	script := writeTasksRepoScript(t, map[string]string{
		"my-feature.md": specContent,
	})

	d := New(gh.New(script), time.Minute, "", dir, nil)
	d.SetTasksRepo("org/tasks")

	var mu sync.Mutex
	callCount := 0
	done := make(chan struct{}, 2)
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description string) error {
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
