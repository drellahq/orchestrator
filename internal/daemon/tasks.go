package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// NewTaskFunc is the function signature for launching a new task.
// sourceRepo and sourceIssue are set when the task is spawned from a tasks-repo issue.
// labels carries GitHub issue labels (e.g. "rhel") that influence sandbox provisioning.
type NewTaskFunc func(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int, labels []string) error

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

// taskNameFromIssue derives a task name from a repo, issue number, and title.
// Format: REPO_NAME-ISSUE_NUMBER-snake_case_title, e.g. "tasks-42-add_dark_mode".
func taskNameFromIssue(repo string, number int, title string) string {
	parts := strings.SplitN(repo, "/", 2)
	repoName := repo
	if len(parts) == 2 {
		repoName = parts[1]
	}
	return fmt.Sprintf("%s-%d-%s", repoName, number, toSnakeCase(title))
}

// toSnakeCase converts a string to snake_case: lowercased, non-alphanumeric
// characters replaced with underscores, leading/trailing/consecutive underscores
// collapsed.
func toSnakeCase(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	// Collapse consecutive underscores and trim edges
	result := b.String()
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	return strings.Trim(result, "_")
}

// taskNameFromSpec derives a task name from a spec filename.
// It strips the .md extension and sanitizes for use as a directory name.
func taskNameFromSpec(specFile string) string {
	name := strings.TrimSuffix(specFile, ".md")
	return name
}

// checkForNewSpecs clones the tasks repo and scans in-progress/ for new spec
// files, spawning tasks for any that haven't been processed yet.
func (d *Daemon) checkForNewSpecs(ctx context.Context) {
	tasksRepo := d.getTasksRepo()
	if tasksRepo == "" {
		return
	}

	log := slog.With("tasks_repo", tasksRepo)

	// Shallow-clone the tasks repo into a temp directory.
	tmpDir, err := os.MkdirTemp("", "specs-clone-*")
	if err != nil {
		log.Warn("Failed to create temp dir for specs clone", "error", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	cloneDir := filepath.Join(tmpDir, "repo")
	if err := d.gh.CloneRepo(ctx, tasksRepo, cloneDir); err != nil {
		log.Debug("Failed to clone tasks repo", "error", err)
		return
	}

	// List files in in-progress/
	inProgressDir := filepath.Join(cloneDir, "in-progress")
	entries, err := os.ReadDir(inProgressDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debug("No in-progress/ directory in tasks repo")
			return
		}
		log.Debug("Failed to read in-progress directory", "error", err)
		return
	}

	if len(entries) == 0 {
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

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		file := entry.Name()

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

		// Read the spec content from the local clone
		content, err := os.ReadFile(filepath.Join(inProgressDir, file))
		if err != nil {
			log.Warn("Failed to read spec content", "spec", file, "error", err)
			continue
		}

		description := string(content)

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

		d.wg.Add(1)
		go func(name, desc string) {
			defer d.wg.Done()
			defer func() {
				d.mu.Lock()
				delete(d.running, name)
				d.mu.Unlock()
			}()

			if err := d.newTaskFunc(context.WithoutCancel(ctx), name, desc, "", 0, nil); err != nil {
				slog.Error("task new failed", "task", name, "error", err)
			}
		}(taskName, description)
	}
}

// checkForNewIssues polls the tasks repo for open GitHub issues and spawns
// tasks for any that haven't been processed yet.
func (d *Daemon) checkForNewIssues(ctx context.Context) {
	tasksRepo := d.getTasksRepo()
	if tasksRepo == "" {
		return
	}

	log := slog.With("tasks_repo", tasksRepo)

	issues, err := d.gh.ListIssues(ctx, tasksRepo)
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

	allowedCommenters := d.getAllowedCommenters()
	allowed := make(map[string]bool, len(allowedCommenters))
	for _, u := range allowedCommenters {
		allowed[u] = true
	}

	for _, issue := range issues {
		if processed.Issues[issue.Number] {
			continue
		}

		if !allowed[issue.User.Login] {
			log.Debug("Issue author not in allowed_commenters, skipping", "issue", issue.Number, "author", issue.User.Login)
			if err := d.gh.ReactToIssue(ctx, tasksRepo, issue.Number, "confused"); err != nil {
				log.Debug("Failed to add confused reaction to issue", "issue", issue.Number, "error", err)
			}
			continue
		}

		taskName := taskNameFromIssue(tasksRepo, issue.Number, issue.Title)

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

		description := issue.Title
		if issue.Body != "" {
			trimmedBody := strings.TrimSpace(issue.Body)
			if strings.HasPrefix(trimmedBody, "---") || strings.HasPrefix(trimmedBody, "```") {
				// Body starts with a task header; keep it at the
				// front so buildNewTaskArgs can parse profile/agent/vars.
				description = issue.Body + "\n\n" + issue.Title
			} else {
				description = issue.Title + "\n\n" + issue.Body
			}
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

		if err := d.gh.ReactToIssue(ctx, tasksRepo, issue.Number, "rocket"); err != nil {
			log.Debug("Failed to add rocket reaction to issue", "issue", issue.Number, "error", err)
		}

		d.wg.Add(1)
		go func(name, desc, repo string, issueNum int, labels []string) {
			defer d.wg.Done()
			defer func() {
				d.mu.Lock()
				delete(d.running, name)
				d.mu.Unlock()
			}()

			if err := d.newTaskFunc(context.WithoutCancel(ctx), name, desc, repo, issueNum, labels); err != nil {
				slog.Error("task new failed", "task", name, "error", err)
			}
		}(taskName, description, tasksRepo, issue.Number, issue.LabelNames())
	}
}

