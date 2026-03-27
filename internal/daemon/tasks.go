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

// processedIssuesFile is the filename used to track which GitHub issues have been picked up.
const processedIssuesFile = "processed_issues.json"

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

// ProcessedIssues tracks which GitHub issues have already been picked up.
type ProcessedIssues struct {
	// Issues maps issue number to true for each issue that has been processed.
	Issues map[int]bool `json:"issues"`
}

// loadProcessedIssues reads the processed issues file from outputDir.
func loadProcessedIssues(outputDir string) (*ProcessedIssues, error) {
	path := filepath.Join(outputDir, processedIssuesFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ProcessedIssues{Issues: make(map[int]bool)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading processed issues: %w", err)
	}
	var pi ProcessedIssues
	if err := json.Unmarshal(data, &pi); err != nil {
		return nil, fmt.Errorf("unmarshaling processed issues: %w", err)
	}
	if pi.Issues == nil {
		pi.Issues = make(map[int]bool)
	}
	return &pi, nil
}

// saveProcessedIssues writes the processed issues file to outputDir.
func saveProcessedIssues(outputDir string, pi *ProcessedIssues) error {
	data, err := json.MarshalIndent(pi, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling processed issues: %w", err)
	}
	path := filepath.Join(outputDir, processedIssuesFile)
	return os.WriteFile(path, data, 0644)
}

// taskNameFromIssue derives a task name from an issue number.
func taskNameFromIssue(repo string, number int) string {
	// Use the repo name and issue number, e.g. "tasks-42"
	parts := strings.SplitN(repo, "/", 2)
	repoName := repo
	if len(parts) == 2 {
		repoName = parts[1]
	}
	return fmt.Sprintf("%s-%d", repoName, number)
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

// checkForNewIssues polls the tasks repo for open GitHub issues and spawns
// tasks for any that haven't been processed yet.
func (d *Daemon) checkForNewIssues(ctx context.Context) {
	if d.tasksRepo == "" {
		return
	}

	log := slog.With("tasks_repo", d.tasksRepo)

	issues, err := d.gh.ListIssues(ctx, d.tasksRepo)
	if err != nil {
		log.Debug("Failed to list issues", "error", err)
		return
	}

	if len(issues) == 0 {
		log.Debug("No open issues")
		return
	}

	// Ensure output dir exists for the processed issues file
	if err := os.MkdirAll(d.outputDir, 0755); err != nil {
		log.Warn("Failed to create output dir", "error", err)
		return
	}

	processed, err := loadProcessedIssues(d.outputDir)
	if err != nil {
		log.Warn("Failed to load processed issues", "error", err)
		return
	}

	for _, issue := range issues {
		if processed.Issues[issue.Number] {
			continue
		}

		taskName := taskNameFromIssue(d.tasksRepo, issue.Number)

		// Check if task is already running — skip but don't mark as
		// processed so it is retried next cycle.
		d.mu.Lock()
		if d.running[taskName] {
			d.mu.Unlock()
			log.Debug("Task already running, skipping issue", "issue", issue.Number)
			continue
		}
		d.mu.Unlock()

		log.Info("Found new issue", "issue", issue.Number, "title", issue.Title, "task", taskName)

		description := issue.Body
		if description == "" {
			description = issue.Title
		}

		// Mark as processed before launching to avoid re-processing
		processed.Issues[issue.Number] = true
		if err := saveProcessedIssues(d.outputDir, processed); err != nil {
			log.Warn("Failed to save processed issues", "error", err)
			continue
		}

		// Set running and launch
		d.mu.Lock()
		if d.running[taskName] {
			d.mu.Unlock()
			log.Debug("Task became running during processing, skipping", "issue", issue.Number)
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

