package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	gh "github.com/drellahq/orchestrator/internal/github"

	"github.com/drellahq/orchestrator/internal/config"
	"github.com/drellahq/orchestrator/internal/daemon"
	"github.com/drellahq/orchestrator/internal/version"
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

	ghRunner := gh.New("")
	botUsername, err := ghRunner.AuthenticatedUser(ctx)
	if err != nil {
		return fmt.Errorf("GitHub CLI not authenticated: %w", err)
	}

	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	versionPath := filepath.Join(cfg.OutputDir, "version.json")
	if err := version.Get().WriteFile(versionPath); err != nil {
		return fmt.Errorf("writing version.json: %w", err)
	}
	slog.Info("Wrote version info", "path", versionPath)

	configCopyPath := filepath.Join(cfg.OutputDir, "config.yaml")
	if err := copyFile(configPath, configCopyPath); err != nil {
		return fmt.Errorf("copying config to output dir: %w", err)
	}
	slog.Info("Copied config", "path", configCopyPath)

	allowedCommenters := cfg.Daemon.AllowedCommenters
	if len(cfg.Daemon.AllowedCommentersOrgs) > 0 {
		resolved, err := config.ResolveAllowedCommenters(ctx, &cfg.Daemon, ghRunner)
		if err != nil {
			slog.Warn("Failed to resolve org commenters", "error", err)
		} else {
			allowedCommenters = resolved.Merged
			if err := config.WriteResolvedCommenters(cfg.OutputDir, resolved); err != nil {
				slog.Warn("Failed to write resolved commenters", "error", err)
			}
			slog.Info("Resolved allowed commenters from orgs",
				"static", len(cfg.Daemon.AllowedCommenters),
				"from_orgs", len(resolved.Merged)-len(cfg.Daemon.AllowedCommenters),
				"total", len(resolved.Merged))
		}
	}

	if len(allowedCommenters) == 0 {
		slog.Warn("daemon.allowed_commenters is empty; no comments will trigger task continue")
	}

	d := daemon.New(ghRunner, interval, configPath, cfg.OutputDir, allowedCommenters, botUsername)

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

			if err := copyFile(configPath, configCopyPath); err != nil {
				slog.Error("Failed to copy config on reload", "error", err)
			}

			reloadedCommenters := newCfg.Daemon.AllowedCommenters
			if len(newCfg.Daemon.AllowedCommentersOrgs) > 0 {
				resolved, err := config.ResolveAllowedCommenters(ctx, &newCfg.Daemon, ghRunner)
				if err != nil {
					slog.Warn("Failed to resolve org commenters on reload", "error", err)
				} else {
					reloadedCommenters = resolved.Merged
					if err := config.WriteResolvedCommenters(newCfg.OutputDir, resolved); err != nil {
						slog.Warn("Failed to write resolved commenters on reload", "error", err)
					}
				}
			}

			d.Reload(newInterval, reloadedCommenters, newCfg.Daemon.TasksRepo)
		}
	}()

	slog.Info("Daemon starting", "interval", interval, "output_dir", cfg.OutputDir, "allowed_commenters", cfg.Daemon.AllowedCommenters, "bot_username", botUsername)
	return d.Run(ctx)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
