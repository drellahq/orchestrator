package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// NewTaskFunc is the function signature for launching a new task.
type NewTaskFunc func(ctx context.Context, taskName, description string) error

// processedSpecsFile is the filename used to track which specs have been picked up.
const processedSpecsFile = "processed_specs.json"

// ProcessedSpecs tracks which spec files have already been picked up.
type ProcessedSpecs struct {
	// Specs maps spec filename to true for each spec that has been processed.
	Specs map[string]bool `json:"specs"`
}

// loadProcessedSpecs reads the processed specs file from outputDir.
func loadProcessedSpecs(outputDir string) (*ProcessedSpecs, error) {
	path := filepath.Join(outputDir, processedSpecsFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ProcessedSpecs{Specs: make(map[string]bool)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading processed specs: %w", err)
	}
	var ps ProcessedSpecs
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("unmarshaling processed specs: %w", err)
	}
	if ps.Specs == nil {
		ps.Specs = make(map[string]bool)
	}
	return &ps, nil
}

// saveProcessedSpecs writes the processed specs file to outputDir.
func saveProcessedSpecs(outputDir string, ps *ProcessedSpecs) error {
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling processed specs: %w", err)
	}
	path := filepath.Join(outputDir, processedSpecsFile)
	return os.WriteFile(path, data, 0644)
}

// taskNameFromSpec derives a task name from a spec filename.
// It strips the .md extension and sanitizes for use as a directory name.
func taskNameFromSpec(specFile string) string {
	name := strings.TrimSuffix(specFile, ".md")
	return name
}

// checkForNewSpecs polls the tasks repo for new spec files in in-progress/
// and spawns tasks for any that haven't been processed yet.
func (d *Daemon) checkForNewSpecs(ctx context.Context) {
	if d.tasksRepo == "" {
		return
	}

	log := slog.With("tasks_repo", d.tasksRepo)

	files, err := d.gh.ListRepoFiles(ctx, d.tasksRepo, "main", "in-progress")
	if err != nil {
		log.Debug("Failed to list in-progress specs", "error", err)
		return
	}

	if len(files) == 0 {
		log.Debug("No specs in in-progress/")
		return
	}

	// Ensure output dir exists for the processed specs file
	if err := os.MkdirAll(d.outputDir, 0755); err != nil {
		log.Warn("Failed to create output dir", "error", err)
		return
	}

	processed, err := loadProcessedSpecs(d.outputDir)
	if err != nil {
		log.Warn("Failed to load processed specs", "error", err)
		return
	}

	for _, file := range files {
		if processed.Specs[file] {
			continue
		}

		if !strings.HasSuffix(file, ".md") {
			continue
		}

		taskName := taskNameFromSpec(file)

		// Check if task is already running
		d.mu.Lock()
		if d.running[taskName] {
			d.mu.Unlock()
			log.Debug("Task already running, skipping spec", "spec", file)
			continue
		}
		d.mu.Unlock()

		log.Info("Found new spec", "spec", file, "task", taskName)

		// Fetch the spec content
		content, err := d.gh.GetFileContent(ctx, d.tasksRepo, "main", "in-progress/"+file)
		if err != nil {
			log.Warn("Failed to fetch spec content", "spec", file, "error", err)
			continue
		}

		// GitHub API returns base64-encoded content
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content, "\n", ""))
		if err != nil {
			log.Warn("Failed to decode spec content", "spec", file, "error", err)
			continue
		}

		description := string(decoded)

		// Mark as processed before launching to avoid re-processing
		processed.Specs[file] = true
		if err := saveProcessedSpecs(d.outputDir, processed); err != nil {
			log.Warn("Failed to save processed specs", "error", err)
			continue
		}

		// Set running and launch
		d.mu.Lock()
		if d.running[taskName] {
			d.mu.Unlock()
			log.Debug("Task became running during processing, skipping", "spec", file)
			continue
		}
		d.running[taskName] = true
		d.mu.Unlock()

		go func(name, desc string) {
			defer func() {
				d.mu.Lock()
				delete(d.running, name)
				d.mu.Unlock()
			}()

			if err := d.newTaskFunc(ctx, name, desc); err != nil {
				slog.Error("task new failed", "task", name, "error", err)
			}
		}(taskName, description)
	}
}
