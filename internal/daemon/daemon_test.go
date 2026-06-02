package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gh "github.com/drellabot/orchestrator/internal/github"
	"github.com/drellabot/orchestrator/internal/prompts"
	"github.com/drellabot/orchestrator/internal/task"
)

func createTaskWithPRs(t *testing.T, outputDir, taskName string, prs []task.PR) {
	t.Helper()
	td, err := task.Create(outputDir, taskName)
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveMetadata(taskName, "test", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, pr := range prs {
		if err := td.AddPR(pr); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoverPRs(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantCount int
	}{
		{
			name:      "empty output dir",
			setup:     func(t *testing.T, dir string) {},
			wantCount: 0,
		},
		{
			name: "task with no PRs",
			setup: func(t *testing.T, dir string) {
				createTaskWithPRs(t, dir, "no-prs", nil)
			},
			wantCount: 0,
		},
		{
			name: "task with one open PR",
			setup: func(t *testing.T, dir string) {
				createTaskWithPRs(t, dir, "one-pr", []task.PR{
					{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"},
				})
			},
			wantCount: 1,
		},
		{
			name: "task with closed PR is skipped",
			setup: func(t *testing.T, dir string) {
				td, err := task.Create(dir, "closed-pr")
				if err != nil {
					t.Fatal(err)
				}
				if err := td.SaveMetadata("closed-pr", "test", "", time.Now()); err != nil {
					t.Fatal(err)
				}
				if err := td.AddPR(task.PR{
					URL: "https://github.com/org/repo/pull/1", Repo: "org/repo",
					Branch: "fix", Base: "main",
				}); err != nil {
					t.Fatal(err)
				}
				// Mark it closed
				if err := td.UpdatePR("https://github.com/org/repo/pull/1", func(pr *task.PR) {
					pr.Closed = true
				}); err != nil {
					t.Fatal(err)
				}
			},
			wantCount: 0,
		},
		{
			name: "multiple tasks with PRs",
			setup: func(t *testing.T, dir string) {
				createTaskWithPRs(t, dir, "task-a", []task.PR{
					{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "a", Base: "main"},
				})
				createTaskWithPRs(t, dir, "task-b", []task.PR{
					{URL: "https://github.com/org/repo/pull/2", Repo: "org/repo", Branch: "b", Base: "main"},
					{URL: "https://github.com/org/repo/pull/3", Repo: "org/repo", Branch: "c", Base: "main"},
				})
			},
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			refs := DiscoverPRs(dir)
			if len(refs) != tt.wantCount {
				t.Errorf("got %d PRRefs, want %d", len(refs), tt.wantCount)
			}
		})
	}
}

func TestDiscoverPRs_PopulatesNumber(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "test-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/42", Repo: "org/repo", Branch: "fix", Base: "main"},
	})

	refs := DiscoverPRs(dir)
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1", len(refs))
	}
	if refs[0].PR.Number != 42 {
		t.Errorf("PR number = %d, want 42", refs[0].PR.Number)
	}
}

func TestFormatCommentsAsPrompt(t *testing.T) {
	tests := []struct {
		name     string
		comments []gh.Comment
		want     string
	}{
		{
			name: "single comment without URL",
			comments: []gh.Comment{
				{ID: 1, Body: "Please fix this", User: gh.CommentUser{Login: "alice"}, CreatedAt: "2025-01-01T00:00:00Z", Type: gh.IssueComment},
			},
			want: prompts.OnPRComment + "\n@alice at 2025-01-01T00:00:00Z:\n\nPlease fix this\n",
		},
		{
			name: "single comment with URL",
			comments: []gh.Comment{
				{ID: 1, Body: "Please fix this", User: gh.CommentUser{Login: "alice"}, CreatedAt: "2025-01-01T00:00:00Z", Type: gh.IssueComment, HTMLURL: "https://github.com/org/repo/pull/42#issuecomment-1"},
			},
			want: prompts.OnPRComment + "\n@alice at 2025-01-01T00:00:00Z (https://github.com/org/repo/pull/42#issuecomment-1):\n\nPlease fix this\n",
		},
		{
			name: "multiple comments with review",
			comments: []gh.Comment{
				{ID: 1, Body: "First", User: gh.CommentUser{Login: "alice"}, CreatedAt: "2025-01-01T00:00:00Z", Type: gh.IssueComment},
				{ID: 2, Body: "Nit here", User: gh.CommentUser{Login: "bob"}, CreatedAt: "2025-01-01T01:00:00Z", Type: gh.ReviewComment, Path: "main.go", HTMLURL: "https://github.com/org/repo/pull/42#discussion_r2"},
			},
			want: prompts.OnPRComment + "\n@alice at 2025-01-01T00:00:00Z:\n\nFirst\n\n---\n\n@bob at 2025-01-01T01:00:00Z on main.go (https://github.com/org/repo/pull/42#discussion_r2):\n\nNit here\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatCommentsAsPrompt(tt.comments)
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFilterNewComments(t *testing.T) {
	comments := []gh.Comment{
		{ID: 10, Body: "old", User: gh.CommentUser{Login: "alice"}},
		{ID: 20, Body: "new from alice", User: gh.CommentUser{Login: "alice"}},
		{ID: 30, Body: "new from stranger", User: gh.CommentUser{Login: "stranger"}},
		{ID: 40, Body: "new from bob", User: gh.CommentUser{Login: "bob"}},
	}

	tests := []struct {
		name              string
		lastCommentID     int64
		allowedCommenters []string
		wantIDs           []int64
	}{
		{
			name:              "filter by ID and allowed users",
			lastCommentID:     15,
			allowedCommenters: []string{"alice", "bob"},
			wantIDs:           []int64{20, 40},
		},
		{
			name:              "all comments old",
			lastCommentID:     100,
			allowedCommenters: []string{"alice"},
			wantIDs:           nil,
		},
		{
			name:              "no allowed commenters",
			lastCommentID:     0,
			allowedCommenters: []string{},
			wantIDs:           nil,
		},
		{
			name:              "from zero baseline",
			lastCommentID:     0,
			allowedCommenters: []string{"alice", "bob"},
			wantIDs:           []int64{10, 20, 40},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterNewComments(comments, tt.lastCommentID, tt.allowedCommenters)
			var gotIDs []int64
			for _, c := range got {
				gotIDs = append(gotIDs, c.ID)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("got %v, want %v", gotIDs, tt.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Errorf("ID[%d] = %d, want %d", i, gotIDs[i], tt.wantIDs[i])
				}
			}
		})
	}
}

func TestFilterRejectedComments(t *testing.T) {
	comments := []gh.Comment{
		{ID: 10, Body: "old", User: gh.CommentUser{Login: "alice"}},
		{ID: 20, Body: "new from alice", User: gh.CommentUser{Login: "alice"}},
		{ID: 30, Body: "new from stranger", User: gh.CommentUser{Login: "stranger"}},
		{ID: 40, Body: "new from bob", User: gh.CommentUser{Login: "bob"}},
		{ID: 50, Body: "new from another stranger", User: gh.CommentUser{Login: "mallory"}},
		{ID: 60, Body: "bot comment", User: gh.CommentUser{Login: "drellabot"}},
	}

	tests := []struct {
		name              string
		lastCommentID     int64
		allowedCommenters []string
		botUsername        string
		wantIDs           []int64
	}{
		{
			name:              "returns only non-allowed new comments",
			lastCommentID:     15,
			allowedCommenters: []string{"alice", "bob"},
			botUsername:        "drellabot",
			wantIDs:           []int64{30, 50},
		},
		{
			name:              "all comments old",
			lastCommentID:     100,
			allowedCommenters: []string{"alice"},
			botUsername:        "drellabot",
			wantIDs:           nil,
		},
		{
			name:              "all commenters allowed",
			lastCommentID:     0,
			allowedCommenters: []string{"alice", "bob", "stranger", "mallory"},
			botUsername:        "drellabot",
			wantIDs:           nil,
		},
		{
			name:              "no allowed commenters means all rejected except bot",
			lastCommentID:     0,
			allowedCommenters: []string{},
			botUsername:        "drellabot",
			wantIDs:           []int64{10, 20, 30, 40, 50},
		},
		{
			name:              "bot comments are never rejected",
			lastCommentID:     0,
			allowedCommenters: []string{"alice"},
			botUsername:        "drellabot",
			wantIDs:           []int64{30, 40, 50},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterRejectedComments(comments, tt.lastCommentID, tt.allowedCommenters, tt.botUsername)
			var gotIDs []int64
			for _, c := range got {
				gotIDs = append(gotIDs, c.ID)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("got %v, want %v", gotIDs, tt.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Errorf("ID[%d] = %d, want %d", i, gotIDs[i], tt.wantIDs[i])
				}
			}
		})
	}
}

func TestMaxCommentID(t *testing.T) {
	tests := []struct {
		name   string
		slices [][]gh.Comment
		want   int64
	}{
		{
			name:   "single slice",
			slices: [][]gh.Comment{{
				{ID: 10}, {ID: 30}, {ID: 20},
			}},
			want: 30,
		},
		{
			name: "multiple slices",
			slices: [][]gh.Comment{
				{{ID: 10}, {ID: 20}},
				{{ID: 50}, {ID: 5}},
			},
			want: 50,
		},
		{
			name:   "empty slices",
			slices: [][]gh.Comment{nil, nil},
			want:   0,
		},
		{
			name:   "no slices",
			slices: nil,
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maxCommentID(tt.slices...)
			if got != tt.want {
				t.Errorf("maxCommentID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestProcessPR_SkipsRunningTask(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "running-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	// writeArgCapture that says PR is open
	script := writeOpenPRScript(t)

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")
	d.SetTaskRunning("running-task", true)

	ref := PRRef{
		TaskName:  "running-task",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	}

	d.ProcessPR(context.Background(), ref)

	// Task should still be running (no new goroutine launched to clear it)
	if !d.IsTaskRunning("running-task") {
		t.Error("expected task to still be running")
	}
}

func TestProcessPR_SkipsClosedPR(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "closed-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	// writeArgCapture that says PR is closed
	script := writeClosedPRScript(t)

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")

	ref := PRRef{
		TaskName:  "closed-task",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	}

	d.ProcessPR(context.Background(), ref)

	// Verify PR is marked closed in state
	td, err := task.Open(dir, "closed-task")
	if err != nil {
		t.Fatal(err)
	}
	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Resources.GitHub.PRs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(state.Resources.GitHub.PRs))
	}
	if !state.Resources.GitHub.PRs[0].Closed {
		t.Error("expected PR to be marked closed")
	}
}

// writePRWithCommentsScript creates a fake gh that returns "open" for PR state
// checks, the given comment JSON for comment fetches, and captures reaction
// API calls to a file for verification.
func writePRWithCommentsScript(t *testing.T, issueComments, reviewComments string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	reactionsFile := filepath.Join(dir, "reactions.txt")

	content := `#!/bin/sh
REACTIONS_FILE="` + reactionsFile + `"
ISSUE_COMMENTS='` + issueComments + `'
REVIEW_COMMENTS='` + reviewComments + `'

# Handle reaction API calls (POST method)
for arg in "$@"; do
  if [ "$arg" = "--method" ]; then
    printf '%s\n' "$*" >> "$REACTIONS_FILE"
    printf '{}'
    exit 0
  fi
done

# PR state check
for arg in "$@"; do
  if [ "$arg" = "--jq" ]; then
    printf 'open'
    exit 0
  fi
done

# Distinguish issue comments from review comments by URL pattern
for arg in "$@"; do
  case "$arg" in
    */issues/*/comments) printf '%s' "$ISSUE_COMMENTS"; exit 0 ;;
    */pulls/*/comments) printf '%s' "$REVIEW_COMMENTS"; exit 0 ;;
  esac
done

printf '[]'
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script, reactionsFile
}

// parseReactions reads the reactions capture file and returns each reaction
// call as a string of args.
func parseReactions(t *testing.T, path string) []string {
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

func TestProcessPR_ReactsRocketOnAllowed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "react-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	commentsJSON := `[{"id":100,"body":"@testbot Please fix this","user":{"login":"alice"},"created_at":"2025-01-01T00:00:00Z"}]`
	script, reactionsFile := writePRWithCommentsScript(t, commentsJSON, "[]")

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")

	done := make(chan struct{}, 1)
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		done <- struct{}{}
		return nil
	})

	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "react-task",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for continueFunc")
	}
	time.Sleep(50 * time.Millisecond)

	reactions := parseReactions(t, reactionsFile)
	if len(reactions) == 0 {
		t.Fatal("expected at least one reaction call")
	}

	// Should contain a rocket reaction for the allowed comment
	found := false
	for _, r := range reactions {
		if strings.Contains(r, "content=rocket") && strings.Contains(r, "issues/comments/100") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected rocket reaction on comment 100, got reactions: %v", reactions)
	}
}

func TestProcessPR_ReactsConfusedOnRejected(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "reject-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	// Two comments: one from allowed alice, one from stranger
	commentsJSON := `[{"id":100,"body":"@testbot allowed","user":{"login":"alice"},"created_at":"2025-01-01T00:00:00Z"},{"id":200,"body":"rejected","user":{"login":"stranger"},"created_at":"2025-01-01T01:00:00Z"}]`
	script, reactionsFile := writePRWithCommentsScript(t, commentsJSON, "[]")

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")

	done := make(chan struct{}, 1)
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		done <- struct{}{}
		return nil
	})

	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "reject-task",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for continueFunc")
	}
	time.Sleep(50 * time.Millisecond)

	reactions := parseReactions(t, reactionsFile)
	if len(reactions) < 2 {
		t.Fatalf("expected at least 2 reaction calls, got %d: %v", len(reactions), reactions)
	}

	// Should have confused on stranger's comment (200)
	foundConfused := false
	for _, r := range reactions {
		if strings.Contains(r, "content=confused") && strings.Contains(r, "issues/comments/200") {
			foundConfused = true
			break
		}
	}
	if !foundConfused {
		t.Errorf("expected confused reaction on comment 200, got: %v", reactions)
	}

	// Should have rocket on alice's comment (100)
	foundRocket := false
	for _, r := range reactions {
		if strings.Contains(r, "content=rocket") && strings.Contains(r, "issues/comments/100") {
			foundRocket = true
			break
		}
	}
	if !foundRocket {
		t.Errorf("expected rocket reaction on comment 100, got: %v", reactions)
	}
}

func TestProcessPR_NoConfusedReactionOnBotComment(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "bot-comment-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	// Three comments: allowed alice, rejected stranger, and the bot itself
	commentsJSON := `[{"id":100,"body":"@testbot allowed","user":{"login":"alice"},"created_at":"2025-01-01T00:00:00Z"},{"id":200,"body":"rejected","user":{"login":"stranger"},"created_at":"2025-01-01T01:00:00Z"},{"id":300,"body":"bot update","user":{"login":"testbot"},"created_at":"2025-01-01T02:00:00Z"}]`
	script, reactionsFile := writePRWithCommentsScript(t, commentsJSON, "[]")

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")

	done := make(chan struct{}, 1)
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		done <- struct{}{}
		return nil
	})

	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "bot-comment-task",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for continueFunc")
	}
	time.Sleep(50 * time.Millisecond)

	reactions := parseReactions(t, reactionsFile)

	// Should have confused on stranger's comment (200) but NOT on bot's comment (300)
	for _, r := range reactions {
		if strings.Contains(r, "issues/comments/300") && strings.Contains(r, "content=confused") {
			t.Errorf("bot comment should not get confused reaction, got: %v", reactions)
		}
	}

	foundConfused := false
	for _, r := range reactions {
		if strings.Contains(r, "content=confused") && strings.Contains(r, "issues/comments/200") {
			foundConfused = true
			break
		}
	}
	if !foundConfused {
		t.Errorf("expected confused reaction on stranger comment 200, got: %v", reactions)
	}
}

func TestProcessPR_AdvancesLastCommentIDPastRejected(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "advance-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	// Only a comment from a non-allowed user
	commentsJSON := `[{"id":300,"body":"from stranger","user":{"login":"stranger"},"created_at":"2025-01-01T00:00:00Z"}]`
	script, _ := writePRWithCommentsScript(t, commentsJSON, "[]")

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")

	called := false
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		called = true
		return nil
	})

	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "advance-task",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	time.Sleep(50 * time.Millisecond)

	// Task should NOT have been launched (no allowed comments)
	if called {
		t.Error("continueFunc should not have been called with only rejected comments")
	}

	// LastCommentID should have advanced past the rejected comment
	td, err := task.Open(dir, "advance-task")
	if err != nil {
		t.Fatal(err)
	}
	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Resources.GitHub.PRs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(state.Resources.GitHub.PRs))
	}
	if state.Resources.GitHub.PRs[0].LastCommentID != 300 {
		t.Errorf("LastCommentID = %d, want 300", state.Resources.GitHub.PRs[0].LastCommentID)
	}
}

func TestBuildNewTaskArgs_WithSourceIssue(t *testing.T) {
	args := buildNewTaskArgs("/etc/config.yaml", "tasks-42-add_dark_mode", "Fix it", "org/tasks", 42)

	assertContains(t, args, "--source-repo", "org/tasks")
	assertContains(t, args, "--source-issue", "42")
}

func TestBuildNewTaskArgs_WithFrontMatter(t *testing.T) {
	description := "---\nprofile: code-review\nrepo: org/repo\npr: 42\n---\n\nReview this pull request."

	args := buildNewTaskArgs("/etc/config.yaml", "my-task", description, "", 0)

	// Should have: task new --config <path> --profile code-review --var ... --var ... <name> <desc>
	assertContains(t, args, "--config", "/etc/config.yaml")
	assertContains(t, args, "--profile", "code-review")

	// Vars should be present (order may vary for map iteration)
	varArgs := collectVarArgs(args)
	if varArgs["PROFILE_REPO"] != "org/repo" {
		t.Errorf("expected PROFILE_REPO=org/repo, got %q", varArgs["PROFILE_REPO"])
	}
	if varArgs["PROFILE_PR"] != "42" {
		t.Errorf("expected PROFILE_PR=42, got %q", varArgs["PROFILE_PR"])
	}

	// Last two args should be task name and stripped description
	if args[len(args)-2] != "my-task" {
		t.Errorf("expected task name as second-to-last arg, got %q", args[len(args)-2])
	}
	if args[len(args)-1] != "Review this pull request." {
		t.Errorf("expected stripped description as last arg, got %q", args[len(args)-1])
	}
}

func TestBuildNewTaskArgs_NoFrontMatter(t *testing.T) {
	description := "Just a regular task description."

	args := buildNewTaskArgs("/etc/config.yaml", "my-task", description, "", 0)

	// Should have: task new --config <path> <name> <desc>
	if len(args) != 6 {
		t.Fatalf("expected 6 args, got %d: %v", len(args), args)
	}
	assertContains(t, args, "--config", "/etc/config.yaml")

	// Should NOT have --profile or --var
	for _, a := range args {
		if a == "--profile" {
			t.Error("unexpected --profile flag")
		}
		if a == "--var" {
			t.Error("unexpected --var flag")
		}
	}

	if args[len(args)-2] != "my-task" {
		t.Errorf("expected task name, got %q", args[len(args)-2])
	}
	if args[len(args)-1] != description {
		t.Errorf("expected original description, got %q", args[len(args)-1])
	}
}

func TestBuildNewTaskArgs_MalformedFrontMatter(t *testing.T) {
	description := "---\n{{invalid yaml\n---\n\nBody."

	args := buildNewTaskArgs("/etc/config.yaml", "my-task", description, "", 0)

	// Should fall back to raw description
	if args[len(args)-1] != description {
		t.Errorf("expected raw description on parse error, got %q", args[len(args)-1])
	}

	// Should NOT have --profile or --var
	for _, a := range args {
		if a == "--profile" {
			t.Error("unexpected --profile flag")
		}
		if a == "--var" {
			t.Error("unexpected --var flag")
		}
	}
}

func TestBuildNewTaskArgs_ProfileOnly(t *testing.T) {
	description := "---\nprofile: deploy\n---\n\nDeploy the service."

	args := buildNewTaskArgs("/etc/config.yaml", "my-task", description, "", 0)

	assertContains(t, args, "--profile", "deploy")

	// No --var flags expected
	for _, a := range args {
		if a == "--var" {
			t.Error("unexpected --var flag when only profile is set")
		}
	}

	if args[len(args)-1] != "Deploy the service." {
		t.Errorf("expected stripped description, got %q", args[len(args)-1])
	}
}

func TestBuildNewTaskArgs_VarsOnly(t *testing.T) {
	description := "---\nrepo: org/repo\n---\n\nBody."

	args := buildNewTaskArgs("/etc/config.yaml", "my-task", description, "", 0)

	// No --profile flag expected
	for _, a := range args {
		if a == "--profile" {
			t.Error("unexpected --profile flag when no profile key")
		}
	}

	varArgs := collectVarArgs(args)
	if varArgs["PROFILE_REPO"] != "org/repo" {
		t.Errorf("expected PROFILE_REPO=org/repo, got %q", varArgs["PROFILE_REPO"])
	}

	if args[len(args)-1] != "Body." {
		t.Errorf("expected stripped description, got %q", args[len(args)-1])
	}
}

// assertContains checks that args contains a flag followed by its value.
func assertContains(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag {
			if i+1 < len(args) && args[i+1] == value {
				return
			}
			t.Errorf("flag %s found but value %q doesn't match (got %q)", flag, value, args[i+1])
			return
		}
	}
	t.Errorf("flag %s not found in args: %v", flag, args)
}

// collectVarArgs collects --var KEY=VALUE pairs into a map.
func collectVarArgs(args []string) map[string]string {
	result := make(map[string]string)
	for i, a := range args {
		if a == "--var" && i+1 < len(args) {
			parts := strings.SplitN(args[i+1], "=", 2)
			if len(parts) == 2 {
				result[parts[0]] = parts[1]
			}
		}
	}
	return result
}

func TestWriteTriggerEntry(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")

	comments := []gh.Comment{
		{
			ID:        100,
			HTMLURL:   "https://github.com/org/repo/pull/42#issuecomment-100",
			Body:      "Please fix the typo",
			User:      gh.CommentUser{Login: "alice"},
			CreatedAt: "2025-01-15T10:00:00Z",
			Type:      gh.IssueComment,
		},
		{
			ID:        200,
			HTMLURL:   "https://github.com/org/repo/pull/42#discussion_r200",
			Body:      "Rename this variable",
			User:      gh.CommentUser{Login: "bob"},
			CreatedAt: "2025-01-15T11:00:00Z",
			Type:      gh.ReviewComment,
			Path:      "main.go",
		},
	}

	if err := WriteTriggerEntry(transcriptPath, comments); err != nil {
		t.Fatalf("WriteTriggerEntry() error: %v", err)
	}

	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("reading transcript: %v", err)
	}

	// Should be valid JSON ending with newline
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var entry struct {
		Type     string `json:"type"`
		Comments []struct {
			User      string `json:"user"`
			CreatedAt string `json:"created_at"`
			HTMLURL   string `json:"html_url"`
			Path      string `json:"path"`
			Body      string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshaling trigger entry: %v", err)
	}

	if entry.Type != "trigger" {
		t.Errorf("type = %q, want %q", entry.Type, "trigger")
	}
	if len(entry.Comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(entry.Comments))
	}
	if entry.Comments[0].User != "alice" {
		t.Errorf("comment[0].user = %q, want %q", entry.Comments[0].User, "alice")
	}
	if entry.Comments[0].HTMLURL != "https://github.com/org/repo/pull/42#issuecomment-100" {
		t.Errorf("comment[0].html_url = %q", entry.Comments[0].HTMLURL)
	}
	if entry.Comments[1].Path != "main.go" {
		t.Errorf("comment[1].path = %q, want %q", entry.Comments[1].Path, "main.go")
	}
}

func TestWriteTriggerEntry_Appends(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")

	// Write some existing content
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"system","subtype":"init"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	comments := []gh.Comment{
		{ID: 1, Body: "test", User: gh.CommentUser{Login: "alice"}, Type: gh.IssueComment},
	}

	if err := WriteTriggerEntry(transcriptPath, comments); err != nil {
		t.Fatalf("WriteTriggerEntry() error: %v", err)
	}

	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (existing + trigger), got %d", len(lines))
	}
	if !strings.Contains(lines[1], `"type":"trigger"`) {
		t.Errorf("second line should be trigger entry: %s", lines[1])
	}
}

func ghNew(bin string) *gh.Runner {
	return gh.New(bin)
}

// writeOpenPRScript creates a fake gh that returns "open" for PR state checks,
// empty arrays for comment fetches, and handles reaction API calls.
func writeOpenPRScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	content := `#!/bin/sh
# Handle reaction API calls (POST method)
for arg in "$@"; do
  if [ "$arg" = "--method" ]; then
    printf '{}'
    exit 0
  fi
done
# Check if this is a PR state check (has --jq)
for arg in "$@"; do
  if [ "$arg" = "--jq" ]; then
    printf 'open'
    exit 0
  fi
done
# Otherwise return empty comments
printf '[]'
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

func writeClosedPRScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	content := `#!/bin/sh
printf 'closed'
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestReload(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script := writeOpenPRScript(t)
	d := New(ghNew(script), time.Minute, "", t.TempDir(), []string{"alice"}, "testbot")
	d.SetTasksRepo("org/tasks")

	// Verify initial values
	if d.getInterval() != time.Minute {
		t.Errorf("initial interval = %v, want %v", d.getInterval(), time.Minute)
	}
	if len(d.getAllowedCommenters()) != 1 || d.getAllowedCommenters()[0] != "alice" {
		t.Errorf("initial allowed_commenters = %v, want [alice]", d.getAllowedCommenters())
	}
	if d.getTasksRepo() != "org/tasks" {
		t.Errorf("initial tasks_repo = %q, want %q", d.getTasksRepo(), "org/tasks")
	}

	// Reload with new values
	d.Reload(30*time.Second, []string{"alice", "bob"}, "org/new-tasks")

	// Verify reloaded values
	if d.getInterval() != 30*time.Second {
		t.Errorf("reloaded interval = %v, want %v", d.getInterval(), 30*time.Second)
	}
	allowed := d.getAllowedCommenters()
	if len(allowed) != 2 || allowed[0] != "alice" || allowed[1] != "bob" {
		t.Errorf("reloaded allowed_commenters = %v, want [alice bob]", allowed)
	}
	if d.getTasksRepo() != "org/new-tasks" {
		t.Errorf("reloaded tasks_repo = %q, want %q", d.getTasksRepo(), "org/new-tasks")
	}
}

func TestProcessPR_UsesReloadedCommenters(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "reload-test", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	// Comment from bob
	commentsJSON := `[{"id":100,"body":"@testbot Please fix this","user":{"login":"bob"},"created_at":"2025-01-01T00:00:00Z"}]`
	script, _ := writePRWithCommentsScript(t, commentsJSON, "[]")

	// Create daemon with only alice allowed
	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")

	done := make(chan struct{}, 1)
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		done <- struct{}{}
		return nil
	})

	// First attempt: bob's comment should be rejected
	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "reload-test",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	time.Sleep(50 * time.Millisecond)

	select {
	case <-done:
		t.Error("continueFunc should not have been called for bob's comment when only alice is allowed")
	default:
	}

	// Reload to allow bob
	d.Reload(time.Minute, []string{"alice", "bob"}, "")

	// Reset LastCommentID to re-process bob's comment
	td, err := task.Open(dir, "reload-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.UpdatePR("https://github.com/org/repo/pull/1", func(pr *task.PR) {
		pr.LastCommentID = 0
	}); err != nil {
		t.Fatal(err)
	}

	// Second attempt: bob's comment should now trigger continueFunc
	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "reload-test",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for continueFunc after reload")
	}
}

func TestRun_WaitsForRunningTasks(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "wait-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	// Comment from allowed user
	commentsJSON := `[{"id":100,"body":"@testbot Do this","user":{"login":"alice"},"created_at":"2025-01-01T00:00:00Z"}]`
	script, _ := writePRWithCommentsScript(t, commentsJSON, "[]")

	d := New(ghNew(script), 10*time.Millisecond, "", dir, []string{"alice"}, "testbot")

	// Set up a continueFunc that blocks on a channel
	unblock := make(chan struct{})
	taskStarted := make(chan struct{})
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		close(taskStarted)
		<-unblock
		return nil
	})

	// Run in background with a cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- d.Run(ctx)
	}()

	// Wait for the task to start
	select {
	case <-taskStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for task to start")
	}

	// Verify task is running
	if d.RunningCount() != 1 {
		t.Errorf("expected 1 running task, got %d", d.RunningCount())
	}

	// Cancel the context (simulates SIGTERM)
	cancel()

	// Give Run() a moment to process the cancellation
	time.Sleep(100 * time.Millisecond)

	// Verify Run() hasn't returned yet (should be waiting for the goroutine)
	select {
	case <-runDone:
		t.Fatal("Run() returned before task finished")
	default:
		// Good - still waiting
	}

	// Unblock the task
	close(unblock)

	// Verify Run() now returns
	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("Run() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after unblocking task")
	}

	// Verify task is no longer running
	if d.RunningCount() != 0 {
		t.Errorf("expected 0 running tasks, got %d", d.RunningCount())
	}
}

func TestRun_StopsPollingOnShutdown(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()

	// Create a fake gh that reports specs
	script := writeOpenPRScript(t)
	d := New(ghNew(script), 10*time.Millisecond, "", dir, []string{"alice"}, "testbot")

	// Set up a slow newTaskFunc that would allow us to detect if it's called
	taskLaunched := make(chan struct{})
	d.SetNewTaskFunc(func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
		taskLaunched <- struct{}{}
		time.Sleep(time.Second)
		return nil
	})

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- d.Run(ctx)
	}()

	// Verify no tasks were launched
	select {
	case <-taskLaunched:
		t.Fatal("task was launched after context cancellation")
	case err := <-runDone:
		if err != nil {
			t.Errorf("Run() returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run() did not exit within 500ms on cancelled context")
	}
}

func TestCleanupSandboxes_DestroysFinishedTasks(t *testing.T) {
	dir := t.TempDir()

	// Task with no PRs and status done
	createTaskWithPRs(t, dir, "done-task", nil)
	td, _ := task.Open(dir, "done-task")
	td.SetStatus(task.StatusDone)

	var destroyed []string
	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.SetDownFunc(func(ctx context.Context, taskName string) error {
		destroyed = append(destroyed, taskName)
		return nil
	})

	d.cleanupSandboxes(context.Background())

	if len(destroyed) != 1 || destroyed[0] != "done-task" {
		t.Errorf("expected [done-task] destroyed, got %v", destroyed)
	}

	// Verify sandbox_destroyed is set
	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.SandboxDestroyed {
		t.Error("expected SandboxDestroyed = true")
	}
}

func TestCleanupSandboxes_SkipsRunningTasks(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "running-task", nil)

	var destroyed []string
	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.SetTaskRunning("running-task", true)
	d.SetDownFunc(func(ctx context.Context, taskName string) error {
		destroyed = append(destroyed, taskName)
		return nil
	})

	d.cleanupSandboxes(context.Background())

	if len(destroyed) != 0 {
		t.Errorf("expected no destruction, got %v", destroyed)
	}
}

func TestCleanupSandboxes_SkipsTasksWithOpenPRs(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "waiting-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"},
	})
	td, _ := task.Open(dir, "waiting-task")
	td.SetStatus(task.StatusWaiting)

	var destroyed []string
	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.SetDownFunc(func(ctx context.Context, taskName string) error {
		destroyed = append(destroyed, taskName)
		return nil
	})

	d.cleanupSandboxes(context.Background())

	if len(destroyed) != 0 {
		t.Errorf("expected no destruction, got %v", destroyed)
	}
}

func TestCleanupSandboxes_SkipsAlreadyDestroyed(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "old-task", nil)
	td, _ := task.Open(dir, "old-task")
	td.SetSandboxDestroyed()

	var destroyed []string
	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.SetDownFunc(func(ctx context.Context, taskName string) error {
		destroyed = append(destroyed, taskName)
		return nil
	})

	d.cleanupSandboxes(context.Background())

	if len(destroyed) != 0 {
		t.Errorf("expected no destruction (already destroyed), got %v", destroyed)
	}
}

func TestCleanupSandboxes_SkipsInProgressStatus(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "active-task", nil)
	td, _ := task.Open(dir, "active-task")
	td.SetStatus(task.StatusInProgress)

	var destroyed []string
	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.SetDownFunc(func(ctx context.Context, taskName string) error {
		destroyed = append(destroyed, taskName)
		return nil
	})

	d.cleanupSandboxes(context.Background())

	if len(destroyed) != 0 {
		t.Errorf("expected no destruction (in_progress status), got %v", destroyed)
	}
}

func TestRecoverOrphanedTasks_WithOpenPRs(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "orphaned-prs", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"},
	})
	td, _ := task.Open(dir, "orphaned-prs")
	td.SetStatus(task.StatusInProgress)

	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.recoverOrphanedTasks()

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != task.StatusWaiting {
		t.Errorf("Status = %q, want %q", state.Status, task.StatusWaiting)
	}
}

func TestRecoverOrphanedTasks_NoPRs(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "orphaned-no-prs", nil)
	td, _ := task.Open(dir, "orphaned-no-prs")
	td.SetStatus(task.StatusInProgress)

	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.recoverOrphanedTasks()

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != task.StatusDone {
		t.Errorf("Status = %q, want %q", state.Status, task.StatusDone)
	}
}

func TestRecoverOrphanedTasks_SkipsRunning(t *testing.T) {
	dir := t.TempDir()
	createTaskWithPRs(t, dir, "running-ip", nil)
	td, _ := task.Open(dir, "running-ip")
	td.SetStatus(task.StatusInProgress)

	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.SetTaskRunning("running-ip", true)
	d.recoverOrphanedTasks()

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != task.StatusInProgress {
		t.Errorf("Status = %q, want %q (should not change for running task)", state.Status, task.StatusInProgress)
	}
}

func TestRecoverOrphanedTasks_SkipsOtherStatuses(t *testing.T) {
	dir := t.TempDir()

	createTaskWithPRs(t, dir, "waiting-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"},
	})
	tdW, _ := task.Open(dir, "waiting-task")
	tdW.SetStatus(task.StatusWaiting)

	createTaskWithPRs(t, dir, "done-task", nil)
	tdD, _ := task.Open(dir, "done-task")
	tdD.SetStatus(task.StatusDone)

	d := New(ghNew(writeOpenPRScript(t)), time.Minute, "", dir, nil, "testbot")
	d.recoverOrphanedTasks()

	stateW, _ := tdW.LoadState()
	if stateW.Status != task.StatusWaiting {
		t.Errorf("waiting task Status = %q, want %q", stateW.Status, task.StatusWaiting)
	}

	stateD, _ := tdD.LoadState()
	if stateD.Status != task.StatusDone {
		t.Errorf("done task Status = %q, want %q", stateD.Status, task.StatusDone)
	}
}

func TestProcessPR_SetsStatusDoneWhenAllPRsClosed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "all-closed", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})
	td, _ := task.Open(dir, "all-closed")
	td.SetStatus(task.StatusWaiting)

	script := writeClosedPRScript(t)
	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "testbot")

	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "all-closed",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != task.StatusDone {
		t.Errorf("Status = %q, want %q", state.Status, task.StatusDone)
	}
}

func TestContainsMention(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		username string
		want     bool
	}{
		{
			name:     "exact mention at start",
			body:     "@drellabot please fix this",
			username: "drellabot",
			want:     true,
		},
		{
			name:     "mention in middle",
			body:     "Hey @drellabot, can you look at this?",
			username: "drellabot",
			want:     true,
		},
		{
			name:     "case-insensitive",
			body:     "Hey @DrellaBot please review",
			username: "drellabot",
			want:     true,
		},
		{
			name:     "case-insensitive username",
			body:     "Hey @drellabot please review",
			username: "DrellaBot",
			want:     true,
		},
		{
			name:     "no mention",
			body:     "Just a regular comment about the code",
			username: "drellabot",
			want:     false,
		},
		{
			name:     "username without @",
			body:     "drellabot should fix this",
			username: "drellabot",
			want:     false,
		},
		{
			name:     "empty username returns false",
			body:     "@drellabot hello",
			username: "",
			want:     false,
		},
		{
			name:     "empty body",
			body:     "",
			username: "drellabot",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsMention(tt.body, tt.username)
			if got != tt.want {
				t.Errorf("ContainsMention(%q, %q) = %v, want %v", tt.body, tt.username, got, tt.want)
			}
		})
	}
}

func TestFilterMentioned(t *testing.T) {
	comments := []gh.Comment{
		{ID: 10, Body: "@drellabot fix this", User: gh.CommentUser{Login: "alice"}},
		{ID: 20, Body: "Just chatting", User: gh.CommentUser{Login: "alice"}},
		{ID: 30, Body: "Hey @DrellaBot review please", User: gh.CommentUser{Login: "bob"}},
	}

	tests := []struct {
		name     string
		username string
		wantIDs  []int64
	}{
		{
			name:     "filters to mentioned only",
			username: "drellabot",
			wantIDs:  []int64{10, 30},
		},
		{
			name:     "empty username returns none",
			username: "",
			wantIDs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterMentioned(comments, tt.username)
			var gotIDs []int64
			for _, c := range got {
				gotIDs = append(gotIDs, c.ID)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("got %v, want %v", gotIDs, tt.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Errorf("ID[%d] = %d, want %d", i, gotIDs[i], tt.wantIDs[i])
				}
			}
		})
	}
}

func TestProcessPR_IgnoresCommentWithoutMention(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "mention-task", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	commentsJSON := `[{"id":100,"body":"Just chatting among humans","user":{"login":"alice"},"created_at":"2025-01-01T00:00:00Z"}]`
	script, reactionsFile := writePRWithCommentsScript(t, commentsJSON, "[]")

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "drellabot")

	called := false
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		called = true
		return nil
	})

	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "mention-task",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	time.Sleep(50 * time.Millisecond)

	if called {
		t.Error("continueFunc should not have been called for comment without mention")
	}

	// No rocket reaction should have been added
	reactions := parseReactions(t, reactionsFile)
	for _, r := range reactions {
		if strings.Contains(r, "content=rocket") {
			t.Errorf("unexpected rocket reaction: %v", reactions)
		}
	}

	// LastCommentID should still advance past the ignored comment
	td, err := task.Open(dir, "mention-task")
	if err != nil {
		t.Fatal(err)
	}
	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Resources.GitHub.PRs[0].LastCommentID != 100 {
		t.Errorf("LastCommentID = %d, want 100", state.Resources.GitHub.PRs[0].LastCommentID)
	}
}

func TestProcessPR_TriggersOnMention(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	createTaskWithPRs(t, dir, "mention-trigger", []task.PR{
		{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main", Number: 1},
	})

	commentsJSON := `[{"id":100,"body":"@drellabot please fix this","user":{"login":"alice"},"created_at":"2025-01-01T00:00:00Z"},{"id":200,"body":"Just a side note","user":{"login":"alice"},"created_at":"2025-01-01T01:00:00Z"}]`
	script, reactionsFile := writePRWithCommentsScript(t, commentsJSON, "[]")

	d := New(ghNew(script), time.Minute, "", dir, []string{"alice"}, "drellabot")

	done := make(chan string, 1)
	d.SetContinueFunc(func(ctx context.Context, taskName, prompt string) error {
		done <- prompt
		return nil
	})

	d.ProcessPR(context.Background(), PRRef{
		TaskName:  "mention-trigger",
		OutputDir: dir,
		PR:        task.PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Number: 1},
	})

	var prompt string
	select {
	case prompt = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for continueFunc")
	}
	time.Sleep(50 * time.Millisecond)

	// Prompt should contain only the mentioned comment
	if !strings.Contains(prompt, "@drellabot please fix this") {
		t.Errorf("prompt should contain mentioned comment, got: %s", prompt)
	}
	if strings.Contains(prompt, "Just a side note") {
		t.Errorf("prompt should not contain non-mentioned comment, got: %s", prompt)
	}

	// Rocket reaction only on comment 100 (mentioned), not 200
	reactions := parseReactions(t, reactionsFile)
	foundRocket100 := false
	foundRocket200 := false
	for _, r := range reactions {
		if strings.Contains(r, "content=rocket") && strings.Contains(r, "issues/comments/100") {
			foundRocket100 = true
		}
		if strings.Contains(r, "content=rocket") && strings.Contains(r, "issues/comments/200") {
			foundRocket200 = true
		}
	}
	if !foundRocket100 {
		t.Errorf("expected rocket reaction on mentioned comment 100, got: %v", reactions)
	}
	if foundRocket200 {
		t.Errorf("unexpected rocket reaction on non-mentioned comment 200, got: %v", reactions)
	}

	// LastCommentID should advance past both comments
	td, err := task.Open(dir, "mention-trigger")
	if err != nil {
		t.Fatal(err)
	}
	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Resources.GitHub.PRs[0].LastCommentID != 200 {
		t.Errorf("LastCommentID = %d, want 200", state.Resources.GitHub.PRs[0].LastCommentID)
	}
}

