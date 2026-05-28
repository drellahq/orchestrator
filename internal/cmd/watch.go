package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/drellabot/orchestrator/internal/daemon"
	gh "github.com/drellabot/orchestrator/internal/github"
	"github.com/spf13/cobra"
)

var watchTimeout string

var taskWatchCmd = &cobra.Command{
	Use:   "watch <task-name>",
	Short: "Poll a task's PRs for new comments (debug tool)",
	Long: `Polls all open PRs associated with a task for new comments from allowed
commenters, prints the formatted prompt that would be sent to 'task continue',
and exits. Does not modify state.json.

Blocks until a new comment is found or --timeout is reached. Without
--timeout, polls indefinitely until interrupted (Ctrl-C).`,
	Args: cobra.ExactArgs(1),
	RunE: runTaskWatch,
}

func init() {
	taskWatchCmd.Flags().StringVar(&watchTimeout, "timeout", "", "stop waiting after this duration (e.g. 30s, 5m)")
}

func runTaskWatch(cmd *cobra.Command, args []string) error {
	taskName := args[0]

	cfg, err := loadConfigAndSetupLogging()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if watchTimeout != "" {
		d, err := time.ParseDuration(watchTimeout)
		if err != nil {
			return fmt.Errorf("parsing --timeout: %w", err)
		}
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, d)
		defer timeoutCancel()
	}

	ghRunner := gh.New("")
	botUsername, err := ghRunner.AuthenticatedUser(ctx)
	if err != nil {
		return fmt.Errorf("GitHub CLI not authenticated: %w", err)
	}

	prompt, err := daemon.WatchTask(ctx, ghRunner, cfg.OutputDir, taskName, cfg.Daemon.AllowedCommenters, botUsername, 5*time.Second)
	if err != nil {
		return err
	}

	fmt.Print(prompt)
	return nil
}
