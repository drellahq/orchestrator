package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drellahq/orchestrator/internal/config"
)

func TestList(t *testing.T) {
	dir := t.TempDir()

	td, err := Create(dir, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveMetadata("alpha", "desc", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := td.SetStatus(StatusWaiting); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0755); err != nil {
		t.Fatal(err)
	}

	summaries, err := List(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	if summaries[0].Name != "alpha" {
		t.Errorf("Name = %q, want alpha", summaries[0].Name)
	}
	if summaries[0].Status != StatusWaiting {
		t.Errorf("Status = %q, want %q", summaries[0].Status, StatusWaiting)
	}

	all, err := List(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}
}

func TestList_MissingOutputDir(t *testing.T) {
	summaries, err := List(t.TempDir()+"/missing", false)
	if err != nil {
		t.Fatal(err)
	}
	if summaries != nil {
		t.Errorf("summaries = %v, want nil", summaries)
	}
}

func TestValidateCleanup(t *testing.T) {
	tests := []struct {
		name    string
		state   *State
		opts    CleanupOpts
		wantErr bool
	}{
		{
			name:  "waiting task ok",
			state: &State{Name: "t", Status: StatusWaiting},
		},
		{
			name:    "in progress blocked",
			state:   &State{Name: "t", Status: StatusInProgress},
			wantErr: true,
		},
		{
			name: "open pr blocked",
			state: &State{
				Name:   "t",
				Status: StatusWaiting,
				Resources: Resources{GitHub: GitHubResources{PRs: []PR{
					{URL: "https://github.com/o/r/pull/1", Closed: false},
				}}},
			},
			wantErr: true,
		},
		{
			name:    "force bypasses guards",
			state:   &State{Name: "t", Status: StatusInProgress},
			opts:    CleanupOpts{Force: true},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCleanup(tt.state, tt.opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateCleanup() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCleanupTaskSandbox(t *testing.T) {
	dir := t.TempDir()
	td, err := Create(dir, "cleanup-me")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveMetadata("cleanup-me", "desc", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := td.SetStatus(StatusWaiting); err != nil {
		t.Fatal(err)
	}
	repoFile := filepath.Join(td.RepoPath(), "file.txt")
	if err := os.WriteFile(repoFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	var destroyed []string
	old := sandboxDestroyer
	sandboxDestroyer = func(ctx context.Context, cfg *config.Config, taskName string) error {
		destroyed = append(destroyed, taskName)
		return nil
	}
	t.Cleanup(func() { sandboxDestroyer = old })

	cfg := &config.Config{SandboxBackend: "podman"}
	if err := CleanupTaskSandbox(context.Background(), cfg, dir, "cleanup-me", CleanupOpts{}); err != nil {
		t.Fatalf("CleanupTaskSandbox: %v", err)
	}
	if len(destroyed) != 1 || destroyed[0] != "cleanup-me" {
		t.Errorf("destroyed = %v, want [cleanup-me]", destroyed)
	}

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.SandboxDestroyed {
		t.Error("expected SandboxDestroyed = true")
	}
	if _, err := os.Stat(td.RepoPath()); !os.IsNotExist(err) {
		t.Error("expected repo directory removed")
	}
}

func TestCleanupTaskSandbox_DryRun(t *testing.T) {
	dir := t.TempDir()
	td, err := Create(dir, "dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveMetadata("dry-run", "desc", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := td.SetStatus(StatusWaiting); err != nil {
		t.Fatal(err)
	}

	called := false
	old := sandboxDestroyer
	sandboxDestroyer = func(ctx context.Context, cfg *config.Config, taskName string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { sandboxDestroyer = old })

	cfg := &config.Config{SandboxBackend: "podman"}
	if err := CleanupTaskSandbox(context.Background(), cfg, dir, "dry-run", CleanupOpts{DryRun: true}); err != nil {
		t.Fatalf("CleanupTaskSandbox: %v", err)
	}
	if called {
		t.Error("DestroySandbox should not run in dry-run mode")
	}
}

func TestCleanupTaskSandbox_RejectsInProgress(t *testing.T) {
	dir := t.TempDir()
	td, err := Create(dir, "busy")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveMetadata("busy", "desc", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := td.SetStatus(StatusInProgress); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{SandboxBackend: "podman"}
	err = CleanupTaskSandbox(context.Background(), cfg, dir, "busy", CleanupOpts{})
	if err == nil {
		t.Fatal("expected error for in_progress task")
	}
}

func TestIsSandboxNotFound(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("podman rm: no container with name or id"), true},
		{errors.New("gjoll down: sandbox not found"), true},
		{errors.New("permission denied"), false},
	}
	for _, tt := range tests {
		if got := isSandboxNotFound(tt.err); got != tt.want {
			t.Errorf("isSandboxNotFound(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
