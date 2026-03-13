package task

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	meta := Metadata{
		Name:        "meta-test",
		Description: "test task description",
		CreatedAt:   now,
	}

	if err := td.SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "meta-test", "metadata.json"))
	if err != nil {
		t.Fatalf("reading metadata.json: %v", err)
	}

	var got Metadata
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshaling metadata: %v", err)
	}

	if got.Name != meta.Name || got.Description != meta.Description {
		t.Errorf("metadata mismatch: got %+v, want %+v", got, meta)
	}
}
