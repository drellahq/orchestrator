package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreate(t *testing.T) {
	tests := []struct {
		name     string
		taskName string
		setup    func(t *testing.T, dir string) // optional pre-create setup
		wantErr  bool
	}{
		{
			name:     "creates directories",
			taskName: "my-task",
		},
		{
			name:     "fails if already exists",
			taskName: "existing-task",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dir, "existing-task"), 0755); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, outputDir)
			}

			td, err := Create(outputDir, tt.taskName)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify directories exist
			for _, sub := range []string{"repo", "conversations"} {
				path := filepath.Join(outputDir, tt.taskName, sub)
				info, err := os.Stat(path)
				if err != nil {
					t.Errorf("directory %s does not exist: %v", sub, err)
				} else if !info.IsDir() {
					t.Errorf("%s is not a directory", sub)
				}
			}

			// Verify path methods
			wantRepo := filepath.Join(outputDir, tt.taskName, "repo")
			if got := td.RepoPath(); got != wantRepo {
				t.Errorf("RepoPath() = %q, want %q", got, wantRepo)
			}

			wantConv := filepath.Join(outputDir, tt.taskName, "conversations")
			if got := td.ConversationsPath(); got != wantConv {
				t.Errorf("ConversationsPath() = %q, want %q", got, wantConv)
			}

			wantTranscript := filepath.Join(outputDir, tt.taskName, "transcript.jsonl")
			if got := td.TranscriptPath(); got != wantTranscript {
				t.Errorf("TranscriptPath() = %q, want %q", got, wantTranscript)
			}
		})
	}
}

func TestOpen(t *testing.T) {
	tests := []struct {
		name     string
		taskName string
		setup    func(t *testing.T, dir string)
		wantErr  bool
	}{
		{
			name:     "opens existing task",
			taskName: "my-task",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if _, err := Create(dir, "my-task"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "fails if not exists",
			taskName: "nonexistent",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, outputDir)
			}

			td, err := Open(outputDir, tt.taskName)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			wantRepo := filepath.Join(outputDir, tt.taskName, "repo")
			if got := td.RepoPath(); got != wantRepo {
				t.Errorf("RepoPath() = %q, want %q", got, wantRepo)
			}
		})
	}
}

func TestTranscriptPathFor(t *testing.T) {
	got := TranscriptPathFor("/output", "my-task")
	want := filepath.Join("/output", "my-task", "transcript.jsonl")
	if got != want {
		t.Errorf("TranscriptPathFor() = %q, want %q", got, want)
	}
}

func TestSaveMetadata(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "meta-test")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("meta-test", "test task description", "", now); err != nil {
		t.Fatalf("SaveMetadata() error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}

	if s.Name != "meta-test" {
		t.Errorf("Name = %q, want %q", s.Name, "meta-test")
	}
	if s.Description != "test task description" {
		t.Errorf("Description = %q, want %q", s.Description, "test task description")
	}
	if !s.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", s.CreatedAt, now)
	}
}

func TestSaveMetadataSetsUpdatedAt(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "updated-test")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("updated-test", "desc", "", now); err != nil {
		t.Fatalf("SaveMetadata() error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}

	if !s.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", s.UpdatedAt, now)
	}
}

func TestTouchUpdatedAt(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "touch-test")
	if err != nil {
		t.Fatal(err)
	}

	created := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := td.SaveMetadata("touch-test", "desc", "", created); err != nil {
		t.Fatal(err)
	}

	updated := time.Now().Truncate(time.Second)
	if err := td.TouchUpdatedAt(updated); err != nil {
		t.Fatalf("TouchUpdatedAt() error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}

	if !s.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt changed: got %v, want %v", s.CreatedAt, created)
	}
	if !s.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v", s.UpdatedAt, updated)
	}
}

func TestTouchUpdatedAtPreservesState(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "touch-preserve")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("touch-preserve", "my desc", "author", now); err != nil {
		t.Fatal(err)
	}
	pr := PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"}
	if err := td.AddPR(pr); err != nil {
		t.Fatal(err)
	}

	later := now.Add(time.Hour)
	if err := td.TouchUpdatedAt(later); err != nil {
		t.Fatal(err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}

	if s.Name != "touch-preserve" {
		t.Errorf("Name = %q, want %q", s.Name, "touch-preserve")
	}
	if s.Description != "my desc" {
		t.Errorf("Description changed")
	}
	if s.Author != "author" {
		t.Errorf("Author changed")
	}
	if len(s.Resources.GitHub.PRs) != 1 {
		t.Errorf("PRs lost: got %d, want 1", len(s.Resources.GitHub.PRs))
	}
}

func TestSaveMetadataWithAuthor(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "author-test")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("author-test", "test task", "Jane Doe <jane@example.com>", now); err != nil {
		t.Fatalf("SaveMetadata() error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}

	if s.Author != "Jane Doe <jane@example.com>" {
		t.Errorf("Author = %q, want %q", s.Author, "Jane Doe <jane@example.com>")
	}
}

func TestSaveMetadataEmptyAuthor(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "no-author-test")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("no-author-test", "test task", "", now); err != nil {
		t.Fatalf("SaveMetadata() error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}

	if s.Author != "" {
		t.Errorf("Author = %q, want empty string", s.Author)
	}

	// Verify omitempty: author key should not appear in JSON
	data, err := os.ReadFile(filepath.Join(outputDir, "no-author-test", "state.json"))
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	if strings.Contains(string(data), `"author"`) {
		t.Errorf("state.json should omit empty author, got: %s", data)
	}
}

func TestSaveSource(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "source-test")
	if err != nil {
		t.Fatal(err)
	}

	if err := td.SaveSource("org/tasks", 42); err != nil {
		t.Fatalf("SaveSource() error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if s.Source == nil {
		t.Fatal("Source is nil")
	}
	if s.Source.TasksRepo != "org/tasks" {
		t.Errorf("TasksRepo = %q, want %q", s.Source.TasksRepo, "org/tasks")
	}
	if s.Source.IssueNumber != 42 {
		t.Errorf("IssueNumber = %d, want 42", s.Source.IssueNumber)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "source-test", "state.json"))
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshaling state.json: %v", err)
	}
	if _, ok := raw["source"]; !ok {
		t.Errorf("state.json should contain source key, got: %s", data)
	}
}

func TestSaveMetadataPreservesExistingState(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "preserve-test")
	if err != nil {
		t.Fatal(err)
	}

	// Add a PR first
	pr := PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"}
	if err := td.AddPR(pr); err != nil {
		t.Fatalf("AddPR() error: %v", err)
	}

	// Save metadata — should preserve the PR
	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("preserve-test", "desc", "", now); err != nil {
		t.Fatalf("SaveMetadata() error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if s.Name != "preserve-test" {
		t.Errorf("Name = %q, want %q", s.Name, "preserve-test")
	}
	if len(s.Resources.GitHub.PRs) != 1 {
		t.Fatalf("expected 1 PR after SaveMetadata, got %d", len(s.Resources.GitHub.PRs))
	}
	got := s.Resources.GitHub.PRs[0]
	if got.URL != pr.URL || got.Repo != pr.Repo || got.Branch != pr.Branch || got.Base != pr.Base {
		t.Errorf("PR mismatch: got %+v, want %+v", got, pr)
	}
}

func TestLoadState_NoFile(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "state-test")
	if err != nil {
		t.Fatal(err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if len(s.Resources.GitHub.PRs) != 0 {
		t.Errorf("expected empty PRs, got %d", len(s.Resources.GitHub.PRs))
	}
}

func TestPRNumberFromURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    int
		wantErr bool
	}{
		{
			name: "standard PR URL",
			url:  "https://github.com/org/repo/pull/42",
			want: 42,
		},
		{
			name: "PR URL with trailing slash",
			url:  "https://github.com/org/repo/pull/99/",
			want: 99,
		},
		{
			name: "PR URL with sub-path",
			url:  "https://github.com/org/repo/pull/7/files",
			want: 7,
		},
		{
			name:    "no pull path",
			url:     "https://github.com/org/repo/issues/5",
			wantErr: true,
		},
		{
			name:    "non-numeric PR number",
			url:     "https://github.com/org/repo/pull/abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PRNumberFromURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("PRNumberFromURL(%q) = %d, want %d", tt.url, got, tt.want)
			}
		})
	}
}

func TestUpdatePR(t *testing.T) {
	tests := []struct {
		name    string
		prURL   string
		prs     []PR
		mutate  func(*PR)
		wantErr bool
		check   func(t *testing.T, prs []PR)
	}{
		{
			name:  "update LastCommentID",
			prURL: "https://github.com/org/repo/pull/1",
			prs: []PR{
				{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"},
			},
			mutate: func(pr *PR) { pr.LastCommentID = 42 },
			check: func(t *testing.T, prs []PR) {
				if prs[0].LastCommentID != 42 {
					t.Errorf("LastCommentID = %d, want 42", prs[0].LastCommentID)
				}
			},
		},
		{
			name:  "mark closed",
			prURL: "https://github.com/org/repo/pull/1",
			prs: []PR{
				{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"},
			},
			mutate: func(pr *PR) { pr.Closed = true },
			check: func(t *testing.T, prs []PR) {
				if !prs[0].Closed {
					t.Error("expected Closed = true")
				}
			},
		},
		{
			name:  "PR not found",
			prURL: "https://github.com/org/repo/pull/999",
			prs: []PR{
				{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"},
			},
			mutate:  func(pr *PR) {},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			td, err := Create(outputDir, "update-test")
			if err != nil {
				t.Fatal(err)
			}
			for _, pr := range tt.prs {
				if err := td.AddPR(pr); err != nil {
					t.Fatal(err)
				}
			}

			err = td.UpdatePR(tt.prURL, tt.mutate)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			state, err := td.LoadState()
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, state.Resources.GitHub.PRs)
		})
	}
}

func TestSetRunning(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "running-test")
	if err != nil {
		t.Fatal(err)
	}

	if err := td.SaveMetadata("running-test", "desc", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := td.SetRunning(true); err != nil {
		t.Fatalf("SetRunning(true) error: %v", err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if !s.Running {
		t.Error("expected Running = true")
	}

	if err := td.SetRunning(false); err != nil {
		t.Fatalf("SetRunning(false) error: %v", err)
	}

	s, err = td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if s.Running {
		t.Error("expected Running = false")
	}

	// Verify omitempty: running key should not appear when false
	data, err := os.ReadFile(filepath.Join(outputDir, "running-test", "state.json"))
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	if strings.Contains(string(data), `"running"`) {
		t.Errorf("state.json should omit running when false, got: %s", data)
	}
}

func TestSetRunningPreservesState(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "preserve-running")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("preserve-running", "my desc", "author", now); err != nil {
		t.Fatal(err)
	}
	pr := PR{URL: "https://github.com/org/repo/pull/1", Repo: "org/repo", Branch: "fix", Base: "main"}
	if err := td.AddPR(pr); err != nil {
		t.Fatal(err)
	}

	if err := td.SetRunning(true); err != nil {
		t.Fatal(err)
	}

	s, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}

	if s.Name != "preserve-running" {
		t.Errorf("Name = %q, want %q", s.Name, "preserve-running")
	}
	if s.Description != "my desc" {
		t.Errorf("Description changed")
	}
	if s.Author != "author" {
		t.Errorf("Author changed")
	}
	if len(s.Resources.GitHub.PRs) != 1 {
		t.Errorf("PRs lost: got %d, want 1", len(s.Resources.GitHub.PRs))
	}
	if !s.Running {
		t.Error("Running should be true")
	}
}

func TestAddPR_PopulatesNumber(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "number-test")
	if err != nil {
		t.Fatal(err)
	}

	pr := PR{URL: "https://github.com/org/repo/pull/42", Repo: "org/repo", Branch: "fix", Base: "main"}
	if err := td.AddPR(pr); err != nil {
		t.Fatal(err)
	}

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Resources.GitHub.PRs[0].Number != 42 {
		t.Errorf("Number = %d, want 42", state.Resources.GitHub.PRs[0].Number)
	}
}

func TestAddPR(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "pr-test")
	if err != nil {
		t.Fatal(err)
	}

	pr1 := PR{
		URL:    "https://github.com/org/repo/pull/1",
		Repo:   "org/repo",
		Branch: "fix-bug",
		Base:   "main",
	}
	if err := td.AddPR(pr1); err != nil {
		t.Fatalf("AddPR() error: %v", err)
	}

	// Verify state was persisted
	s, err := td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if len(s.Resources.GitHub.PRs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(s.Resources.GitHub.PRs))
	}
	gotPR1 := s.Resources.GitHub.PRs[0]
	if gotPR1.URL != pr1.URL || gotPR1.Repo != pr1.Repo || gotPR1.Branch != pr1.Branch || gotPR1.Base != pr1.Base {
		t.Errorf("PR mismatch: got %+v, want %+v", gotPR1, pr1)
	}

	// Add a second PR
	pr2 := PR{
		URL:    "https://github.com/org/repo/pull/2",
		Repo:   "org/repo",
		Branch: "add-feature",
		Base:   "main",
	}
	if err := td.AddPR(pr2); err != nil {
		t.Fatalf("AddPR() second call error: %v", err)
	}

	s, err = td.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error: %v", err)
	}
	if len(s.Resources.GitHub.PRs) != 2 {
		t.Fatalf("expected 2 PRs, got %d", len(s.Resources.GitHub.PRs))
	}
	gotPR2 := s.Resources.GitHub.PRs[1]
	if gotPR2.URL != pr2.URL || gotPR2.Repo != pr2.Repo || gotPR2.Branch != pr2.Branch || gotPR2.Base != pr2.Base {
		t.Errorf("second PR mismatch: got %+v, want %+v", gotPR2, pr2)
	}

	// Verify on-disk JSON structure
	data, err := os.ReadFile(filepath.Join(outputDir, "pr-test", "state.json"))
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshaling state.json: %v", err)
	}
	resources, ok := raw["resources"].(map[string]any)
	if !ok {
		t.Fatal("state.json missing resources key")
	}
	github, ok := resources["github"].(map[string]any)
	if !ok {
		t.Fatal("state.json missing resources.github key")
	}
	prs, ok := github["prs"].([]any)
	if !ok {
		t.Fatal("state.json missing resources.github.prs key")
	}
	if len(prs) != 2 {
		t.Errorf("state.json has %d PRs, want 2", len(prs))
	}
}
