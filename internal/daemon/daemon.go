package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gh "github.com/drellabot/orchestrator/internal/github"
	"github.com/drellabot/orchestrator/internal/profile"
	"github.com/drellabot/orchestrator/internal/prompts"
	"github.com/drellabot/orchestrator/internal/task"
)

// PRRef ties a task name to one of its PRs.
type PRRef struct {
	TaskName  string
	OutputDir string
	PR        task.PR
}

// ContinueFunc is the function signature for launching task continue.
type ContinueFunc func(ctx context.Context, taskName, prompt string) error

// Daemon polls GitHub PRs for new comments and triggers task continue.
// It also monitors a tasks repo for new specs in in-progress/ and spawns
// new tasks for them.
type Daemon struct {
	gh           *gh.Runner
	configPath   string
	outputDir    string
	continueFunc ContinueFunc
	newTaskFunc  NewTaskFunc
	mu           sync.Mutex
	running      map[string]bool
	wg           sync.WaitGroup

	// Reloadable fields protected by configMu
	configMu          sync.RWMutex
	interval          time.Duration
	allowedCommenters []string
	tasksRepo         string
}

// New creates a Daemon.
func New(ghRunner *gh.Runner, interval time.Duration, configPath, outputDir string, allowedCommenters []string) *Daemon {
	d := &Daemon{
		gh:                ghRunner,
		interval:          interval,
		configPath:        configPath,
		outputDir:         outputDir,
		allowedCommenters: allowedCommenters,
		running:           make(map[string]bool),
	}
	d.continueFunc = d.defaultContinueFunc
	d.newTaskFunc = d.defaultNewTaskFunc
	return d
}

// SetTasksRepo sets the tasks repo to monitor for new specs.
func (d *Daemon) SetTasksRepo(repo string) {
	d.configMu.Lock()
	defer d.configMu.Unlock()
	d.tasksRepo = repo
}

// getInterval returns the current poll interval.
func (d *Daemon) getInterval() time.Duration {
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	return d.interval
}

// getAllowedCommenters returns the current allowed commenters list.
func (d *Daemon) getAllowedCommenters() []string {
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	return d.allowedCommenters
}

// getTasksRepo returns the current tasks repo.
func (d *Daemon) getTasksRepo() string {
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	return d.tasksRepo
}

// Reload updates the daemon's reloadable configuration.
func (d *Daemon) Reload(interval time.Duration, allowedCommenters []string, tasksRepo string) {
	d.configMu.Lock()
	defer d.configMu.Unlock()
	d.interval = interval
	d.allowedCommenters = allowedCommenters
	d.tasksRepo = tasksRepo
	slog.Info("Configuration reloaded", "interval", interval, "allowed_commenters", allowedCommenters, "tasks_repo", tasksRepo)
}

// Run is the main polling loop. It discovers PRs, iterates through them
// round-robin, and re-discovers after each full cycle. It also checks
// the tasks repo for new specs in in-progress/.
func (d *Daemon) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		// Check for new specs and issues in the tasks repo
		d.checkForNewSpecs(ctx)
		d.checkForNewIssues(ctx)

		refs := DiscoverPRs(d.outputDir)
		interval := d.getInterval()
		if len(refs) == 0 {
			slog.Info("No open PRs found, waiting before re-discovery", "interval", interval)
			if err := sleep(ctx, interval); err != nil {
				break
			}
			continue
		}

		slog.Info("Discovered PRs", "count", len(refs))

		perPR := interval / time.Duration(len(refs))
		const minInterval = 5 * time.Second
		if perPR < minInterval {
			perPR = minInterval
		}

		for _, ref := range refs {
			if ctx.Err() != nil {
				break
			}

			d.processPR(ctx, ref)

			if err := sleep(ctx, perPR); err != nil {
				break
			}
		}
	}

	// Wait for running tasks to finish
	d.mu.Lock()
	count := len(d.running)
	d.mu.Unlock()
	if count > 0 {
		slog.Info("Shutting down, waiting for running tasks to finish", "count", count)
		d.wg.Wait()
		slog.Info("All tasks finished, exiting")
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// DiscoverPRs scans all task directories for state.json files with open PRs.
func DiscoverPRs(outputDir string) []PRRef {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		slog.Debug("Cannot read output dir", "dir", outputDir, "error", err)
		return nil
	}

	var refs []PRRef
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskName := entry.Name()
		td, err := task.Open(outputDir, taskName)
		if err != nil {
			continue
		}
		state, err := td.LoadState()
		if err != nil {
			slog.Debug("Cannot load state", "task", taskName, "error", err)
			continue
		}
		for _, pr := range state.Resources.GitHub.PRs {
			if pr.Closed {
				continue
			}
			// Ensure Number is populated
			if pr.Number == 0 && pr.URL != "" {
				if n, err := task.PRNumberFromURL(pr.URL); err == nil {
					pr.Number = n
				}
			}
			refs = append(refs, PRRef{
				TaskName:  taskName,
				OutputDir: outputDir,
				PR:        pr,
			})
		}
	}
	return refs
}

func (d *Daemon) processPR(ctx context.Context, ref PRRef) {
	log := slog.With("task", ref.TaskName, "pr", ref.PR.URL)

	if ref.PR.Number == 0 {
		log.Warn("PR has no number, skipping")
		return
	}

	// Check if PR is still open
	open, err := d.gh.IsPROpen(ctx, ref.PR.Repo, ref.PR.Number)
	if err != nil {
		log.Warn("Failed to check PR state", "error", err)
		return
	}
	if !open {
		log.Info("PR is closed, marking as closed")
		td, err := task.Open(ref.OutputDir, ref.TaskName)
		if err != nil {
			log.Warn("Failed to open task dir", "error", err)
			return
		}
		if err := td.UpdatePR(ref.PR.URL, func(pr *task.PR) {
			pr.Closed = true
		}); err != nil {
			log.Warn("Failed to mark PR as closed", "error", err)
		}
		return
	}

	// Check if task is already running — hold the lock through to
	// setting running=true to avoid a TOCTOU race if ProcessPR is
	// ever called concurrently for the same task.
	d.mu.Lock()
	if d.running[ref.TaskName] {
		d.mu.Unlock()
		log.Debug("Task already running, skipping")
		return
	}
	d.mu.Unlock()

	// Fetch comments
	comments, err := d.gh.FetchAllComments(ctx, ref.PR.Repo, ref.PR.Number)
	if err != nil {
		log.Warn("Failed to fetch comments", "error", err)
		return
	}

	// Partition new comments into allowed and rejected
	allowedCommenters := d.getAllowedCommenters()
	newComments := FilterNewComments(comments, ref.PR.LastCommentID, allowedCommenters)
	rejectedComments := FilterRejectedComments(comments, ref.PR.LastCommentID, allowedCommenters)

	if len(newComments) == 0 && len(rejectedComments) == 0 {
		log.Debug("No new comments")
		return
	}

	// React confused to comments from non-allowed users
	d.reactToComments(ctx, ref.PR.Repo, rejectedComments, "confused")

	// Advance LastCommentID past all new comments (both allowed and rejected)
	// so that rejected comments are not re-processed every poll cycle.
	maxID := maxCommentID(newComments, rejectedComments)
	td, err := task.Open(ref.OutputDir, ref.TaskName)
	if err != nil {
		log.Warn("Failed to open task dir", "error", err)
		return
	}
	if err := td.UpdatePR(ref.PR.URL, func(pr *task.PR) {
		pr.LastCommentID = maxID
	}); err != nil {
		log.Warn("Failed to update LastCommentID", "error", err)
		return
	}

	if len(newComments) == 0 {
		log.Debug("No new comments from allowed users")
		return
	}

	log.Info("Found new comments", "count", len(newComments))

	prompt := FormatCommentsAsPrompt(newComments)

	// Re-check and set running atomically — between the first check
	// above and here, another goroutine could have started a continue
	// for the same task (e.g. via concurrent ProcessPR calls).
	d.mu.Lock()
	if d.running[ref.TaskName] {
		d.mu.Unlock()
		log.Debug("Task became running during processing, skipping")
		return
	}
	d.running[ref.TaskName] = true
	d.mu.Unlock()

	// React rocket to allowed comments just before handoff
	d.reactToComments(ctx, ref.PR.Repo, newComments, "rocket")

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.running, ref.TaskName)
			d.mu.Unlock()
		}()

		if err := d.continueFunc(context.WithoutCancel(ctx), ref.TaskName, prompt); err != nil {
			log.Error("task continue failed", "error", err)
		}
	}()
}

// FilterNewComments returns comments with ID > lastCommentID and user in
// the allowed list. The input must be sorted by ID.
func FilterNewComments(comments []gh.Comment, lastCommentID int64, allowedCommenters []string) []gh.Comment {
	allowed := make(map[string]bool, len(allowedCommenters))
	for _, u := range allowedCommenters {
		allowed[u] = true
	}

	var result []gh.Comment
	for _, c := range comments {
		if c.ID <= lastCommentID {
			continue
		}
		if !allowed[c.User.Login] {
			continue
		}
		result = append(result, c)
	}
	return result
}

// FilterRejectedComments returns comments with ID > lastCommentID whose user
// is NOT in the allowed list. This is the complement of FilterNewComments for
// the same ID-filtered set.
func FilterRejectedComments(comments []gh.Comment, lastCommentID int64, allowedCommenters []string) []gh.Comment {
	allowed := make(map[string]bool, len(allowedCommenters))
	for _, u := range allowedCommenters {
		allowed[u] = true
	}

	var result []gh.Comment
	for _, c := range comments {
		if c.ID <= lastCommentID {
			continue
		}
		if !allowed[c.User.Login] {
			result = append(result, c)
		}
	}
	return result
}

// maxCommentID returns the highest comment ID across multiple slices.
func maxCommentID(slices ...[]gh.Comment) int64 {
	var max int64
	for _, s := range slices {
		for _, c := range s {
			if c.ID > max {
				max = c.ID
			}
		}
	}
	return max
}

// reactToComments adds a reaction to each comment, logging failures without
// returning an error (reactions are best-effort and must not block task dispatch).
func (d *Daemon) reactToComments(ctx context.Context, repo string, comments []gh.Comment, reaction string) {
	for _, c := range comments {
		if err := d.gh.ReactToComment(ctx, repo, c.ID, c.Type, reaction); err != nil {
			slog.Debug("Failed to add reaction", "comment_id", c.ID, "reaction", reaction, "error", err)
		}
	}
}

// FormatCommentsAsPrompt formats comments as a chronological prompt.
func FormatCommentsAsPrompt(comments []gh.Comment) string {
	// Ensure sorted by ID
	sort.Slice(comments, func(i, j int) bool { return comments[i].ID < comments[j].ID })

	var sb strings.Builder
	sb.WriteString(prompts.OnPRComment)
	sb.WriteString("\n")
	for i, c := range comments {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		header := fmt.Sprintf("@%s at %s", c.User.Login, c.CreatedAt)
		if c.Type == gh.ReviewComment && c.Path != "" {
			header += fmt.Sprintf(" on %s", c.Path)
		}
		sb.WriteString(header + ":\n\n")
		sb.WriteString(c.Body)
		sb.WriteString("\n")
	}
	return sb.String()
}

func (d *Daemon) defaultContinueFunc(ctx context.Context, taskName, prompt string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}

	args := []string{"task", "continue", "--config", d.configPath, taskName, prompt}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("Launching task continue", "task", taskName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("task continue %s: %w", taskName, err)
	}
	return nil
}

func (d *Daemon) defaultNewTaskFunc(ctx context.Context, taskName, description, sourceRepo string, sourceIssue int) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting executable path: %w", err)
	}

	args := buildNewTaskArgs(d.configPath, taskName, description, sourceRepo, sourceIssue)

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	slog.Info("Launching task new", "task", taskName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("task new %s: %w", taskName, err)
	}
	return nil
}

// buildNewTaskArgs parses front matter from the description and returns
// the argument list for "task new". If front matter contains a profile,
// --profile is added. Non-profile keys become --var KEY=VALUE flags.
// sourceRepo and sourceIssue are set when spawning from a tasks-repo issue.
func buildNewTaskArgs(configPath, taskName, description, sourceRepo string, sourceIssue int) []string {
	args := []string{"task", "new", "--config", configPath}

	profileName, vars, strippedDesc, fmErr := profile.ParseFrontMatter(description)
	if fmErr != nil {
		slog.Warn("Failed to parse front matter, using raw description", "task", taskName, "error", fmErr)
		strippedDesc = description
	}
	if profileName != "" {
		args = append(args, "--profile", profileName)
	}
	for k, v := range vars {
		args = append(args, "--var", k+"="+v)
	}
	if sourceRepo != "" && sourceIssue > 0 {
		args = append(args, "--source-repo", sourceRepo, "--source-issue", strconv.Itoa(sourceIssue))
	}

	args = append(args, taskName, strippedDesc)
	return args
}

// ListTaskDirs returns the names of all task directories in outputDir.
func ListTaskDirs(outputDir string) ([]string, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// watchPR holds the baseline state for polling a single PR.
type watchPR struct {
	Repo     string
	Number   int
	Baseline int64
}

// WatchTask polls all open PRs for a task for new comments from allowed
// commenters. It returns the formatted prompt on the first new comment(s)
// found across any PR.
func WatchTask(ctx context.Context, ghRunner *gh.Runner, outputDir, taskName string, allowedCommenters []string, pollInterval time.Duration) (string, error) {
	td, err := task.Open(outputDir, taskName)
	if err != nil {
		return "", fmt.Errorf("opening task: %w", err)
	}

	state, err := td.LoadState()
	if err != nil {
		return "", fmt.Errorf("loading state: %w", err)
	}

	var prs []watchPR
	for _, pr := range state.Resources.GitHub.PRs {
		if pr.Closed {
			continue
		}
		number := pr.Number
		if number == 0 && pr.URL != "" {
			if n, err := task.PRNumberFromURL(pr.URL); err == nil {
				number = n
			}
		}
		if number == 0 {
			continue
		}
		prs = append(prs, watchPR{
			Repo:     pr.Repo,
			Number:   number,
			Baseline: pr.LastCommentID,
		})
	}

	if len(prs) == 0 {
		return "", fmt.Errorf("no open PRs found for task %s", taskName)
	}

	slog.Info("Watching PRs", "task", taskName, "count", len(prs))

	for {
		for _, pr := range prs {
			comments, err := ghRunner.FetchAllComments(ctx, pr.Repo, pr.Number)
			if err != nil {
				return "", fmt.Errorf("fetching comments for %s#%d: %w", pr.Repo, pr.Number, err)
			}

			newComments := FilterNewComments(comments, pr.Baseline, allowedCommenters)
			if len(newComments) > 0 {
				return FormatCommentsAsPrompt(newComments), nil
			}
		}

		if err := sleep(ctx, pollInterval); err != nil {
			return "", ctx.Err()
		}
	}
}

// IsTaskRunning reports whether the task is tracked as running.
func (d *Daemon) IsTaskRunning(taskName string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running[taskName]
}

// SetTaskRunning sets a task's running state (for testing).
func (d *Daemon) SetTaskRunning(taskName string, running bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if running {
		d.running[taskName] = true
	} else {
		delete(d.running, taskName)
	}
}

// SetContinueFunc overrides the function used to launch task continue (for testing).
func (d *Daemon) SetContinueFunc(fn ContinueFunc) {
	d.continueFunc = fn
}

// SetNewTaskFunc overrides the function used to launch new tasks (for testing).
func (d *Daemon) SetNewTaskFunc(fn NewTaskFunc) {
	d.newTaskFunc = fn
}

// ProcessPR is an exported wrapper for testing.
func (d *Daemon) ProcessPR(ctx context.Context, ref PRRef) {
	d.processPR(ctx, ref)
}

// RunningCount returns the number of tasks currently running.
func (d *Daemon) RunningCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.running)
}
