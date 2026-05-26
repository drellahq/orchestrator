package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/drellabot/orchestrator/internal/config"
	"github.com/drellabot/orchestrator/internal/daemon"
	"github.com/drellabot/orchestrator/internal/vcs"
	"github.com/spf13/cobra"
)

var daemonInterval string

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Poll GitHub PRs for new comments and trigger task continue",
	Long: `The daemon polls all open PRs tracked in task state files, checks for new
comments from allowed users, and automatically runs 'task continue' with
those comments as the prompt.`,
	RunE: runDaemon,
}

func init() {
	daemonCmd.Flags().StringVar(&daemonInterval, "interval", "", "poll interval (e.g. 60s, 5m); overrides config")
}

func runDaemon(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfigAndSetupLogging()
	if err != nil {
		return err
	}

	interval := 60 * time.Second
	// Config takes precedence over default
	if cfg.Daemon.PollInterval != "" {
		parsed, err := time.ParseDuration(cfg.Daemon.PollInterval)
		if err != nil {
			return fmt.Errorf("parsing daemon.poll_interval: %w", err)
		}
		interval = parsed
	}
	// Flag takes precedence over config
	if daemonInterval != "" {
		parsed, err := time.ParseDuration(daemonInterval)
		if err != nil {
			return fmt.Errorf("parsing --interval: %w", err)
		}
		interval = parsed
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	vcsProvider, err := vcs.NewProvider(cfg.VCSProvider)
	if err != nil {
		return fmt.Errorf("creating VCS provider: %w", err)
	}
	if _, err := vcsProvider.AuthenticatedUser(ctx); err != nil {
		return fmt.Errorf("VCS provider not authenticated: %w", err)
	}

	if len(cfg.Daemon.AllowedCommenters) == 0 {
		slog.Warn("daemon.allowed_commenters is empty; no comments will trigger task continue")
	}

	d := daemon.New(vcsProvider, interval, configPath, cfg.OutputDir, cfg.Daemon.AllowedCommenters)

	if cfg.Daemon.TasksRepo != "" {
		d.SetTasksRepo(cfg.Daemon.TasksRepo)
		slog.Info("Tasks repo monitoring enabled", "tasks_repo", cfg.Daemon.TasksRepo)
	}

	// Set up SIGHUP handler for config reload
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	go func() {
		for range sighup {
			slog.Info("Received SIGHUP, reloading configuration")
			newCfg, err := config.Load(configPath)
			if err != nil {
				slog.Error("Failed to reload config", "error", err)
				continue
			}

			newInterval := interval // keep the current default
			if newCfg.Daemon.PollInterval != "" {
				parsed, err := time.ParseDuration(newCfg.Daemon.PollInterval)
				if err != nil {
					slog.Error("Failed to parse reloaded poll_interval", "error", err)
					continue
				}
				newInterval = parsed
			}
			// CLI flag still takes precedence
			if daemonInterval != "" {
				parsed, err := time.ParseDuration(daemonInterval)
				if err != nil {
					slog.Error("Failed to parse --interval on reload", "error", err)
					continue
				}
				newInterval = parsed
			}

			d.Reload(newInterval, newCfg.Daemon.AllowedCommenters, newCfg.Daemon.TasksRepo)
		}
	}()

	slog.Info("Daemon starting", "interval", interval, "output_dir", cfg.OutputDir, "allowed_commenters", cfg.Daemon.AllowedCommenters)
	return d.Run(ctx)
}
