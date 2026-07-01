package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drellahq/orchestrator/internal/config"
	"github.com/drellahq/orchestrator/internal/task"
	"github.com/goccy/go-yaml"
)

func TestRunTaskRm_DryRun(t *testing.T) {
	dir := t.TempDir()
	td, err := task.Create(dir, "rm-me")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveMetadata("rm-me", "desc", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := td.SetStatus(task.StatusWaiting); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "orchestrator.yaml")
	cfg := &config.Config{OutputDir: dir, SandboxBackend: "podman"}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	oldConfig := configPath
	oldDryRun := taskRmDryRun
	configPath = cfgPath
	taskRmDryRun = true
	t.Cleanup(func() {
		configPath = oldConfig
		taskRmDryRun = oldDryRun
	})

	if err := runTaskRm(taskRmCmd, []string{"rm-me"}); err != nil {
		t.Fatal(err)
	}

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.SandboxDestroyed {
		t.Error("dry-run should not mark sandbox destroyed")
	}
}
