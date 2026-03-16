package cmd

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/drellabot/orchestrator/internal/config"
	gh "github.com/drellabot/orchestrator/internal/github"
	"github.com/drellabot/orchestrator/internal/gjoll"
	"github.com/drellabot/orchestrator/internal/logging"
	mcpserver "github.com/drellabot/orchestrator/internal/mcp"
	"github.com/drellabot/orchestrator/internal/task"
	"github.com/spf13/cobra"
)

//go:embed prompt.md
var promptContent string

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage tasks",
}

var taskNewCmd = &cobra.Command{
	Use:   "new <task-name> <task-description...>",
	Short: "Run a new task in a sandboxed Claude instance",
	Long: `Provisions a sandbox VM via gjoll, starts an MCP server for code pulling,
launches Claude with the task description, and archives the results.`,
	Args: cobra.MinimumNArgs(2),
	RunE: runTask,
}

func init() {
	taskCmd.AddCommand(taskNewCmd)
}

func runTask(cmd *cobra.Command, args []string) error {
	taskName := args[0]
	taskDescription := strings.Join(args[1:], " ")

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Set up logging
	logger := logging.SetupLogger(cfg.SlackWebhook, verbose)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if len(cfg.AllowedRepos) == 0 {
		slog.Warn("allowed_repos is empty; open_pr and update_pr tools will not be available")
	}

	ghRunner := gh.New("")
	if _, err := ghRunner.AuthenticatedUser(context.Background()); err != nil {
		slog.Warn("GitHub CLI not authenticated; open_pr and update_pr tools will not be available", "error", err)
	}

	slog.Info("Task started", "task", taskName)

	// Create task directory
	taskDir, err := task.Create(cfg.OutputDir, taskName)
	if err != nil {
		return fmt.Errorf("creating task directory: %w", err)
	}

	if err := taskDir.SaveMetadata(task.Metadata{
		Name:        taskName,
		Description: taskDescription,
		CreatedAt:   time.Now(),
	}); err != nil {
		return fmt.Errorf("saving metadata: %w", err)
	}

	runner := gjoll.New("")

	// Start MCP server
	mcpSrv := mcpserver.New(logger, taskName, taskDir, runner, ghRunner, cfg.AllowedRepos)
	if err := mcpSrv.Start(); err != nil {
		return fmt.Errorf("starting MCP server: %w", err)
	}
	defer func() { _ = mcpSrv.Stop(context.Background()) }()

	slog.Debug("MCP server started", "task", taskName, "port", mcpserver.MCPPort)

	// Provision sandbox
	tfPath, err := filepath.Abs(cfg.GjollEnv)
	if err != nil {
		return fmt.Errorf("resolving tf path: %w", err)
	}

	slog.Info("Provisioning sandbox", "task", taskName)
	if err := runner.Up(ctx, taskName, tfPath); err != nil {
		return fmt.Errorf("provisioning sandbox: %w", err)
	}

	// After successful provisioning, ensure cleanup on exit
	defer func() {
		slog.Debug("Copying transcript", "task", taskName)
		if copyErr := runner.Cp(context.Background(), taskName, ":~/transcript.jsonl", taskDir.TranscriptPath()); copyErr != nil {
			slog.Warn("Failed to copy transcript", "task", taskName, "error", copyErr)
		}

		slog.Debug("Copying conversations", "task", taskName)
		if copyErr := runner.Cp(context.Background(), taskName, ":~/.claude/", taskDir.ConversationsPath()); copyErr != nil {
			slog.Warn("Failed to copy conversations", "task", taskName, "error", copyErr)
		}

		slog.Debug("Stopping sandbox", "task", taskName)
		if stopErr := runner.Stop(context.Background(), taskName); stopErr != nil {
			slog.Warn("Failed to stop sandbox", "task", taskName, "error", stopErr)
		}
	}()

	slog.Info("Sandbox provisioned", "task", taskName)

	// Post-provision setup
	if err := setupSandbox(ctx, runner, taskName); err != nil {
		return fmt.Errorf("setting up sandbox: %w", err)
	}

	slog.Debug("Sandbox setup complete", "task", taskName)

	// Write a run script and copy it to the VM to avoid quoting issues
	// with SSH + shell escaping
	slog.Info("Running Claude", "task", taskName)
	escapedDesc := strings.ReplaceAll(taskDescription, "'", "'\\''")
	runScript := fmt.Sprintf(`#!/bin/bash
cd ~/project
stdbuf -oL claude --dangerously-skip-permissions -p --verbose \
  --output-format stream-json '%s' \
  </dev/null | stdbuf -oL tee ~/transcript.jsonl
`, escapedDesc)

	tmpRun, err := os.CreateTemp("", "run-claude-*.sh")
	if err != nil {
		return fmt.Errorf("creating run script: %w", err)
	}
	defer os.Remove(tmpRun.Name())
	if _, err := tmpRun.WriteString(runScript); err != nil {
		tmpRun.Close()
		return fmt.Errorf("writing run script: %w", err)
	}
	tmpRun.Close()

	if err := runner.Cp(ctx, taskName, tmpRun.Name(), ":/tmp/run-claude.sh"); err != nil {
		return fmt.Errorf("copying run script: %w", err)
	}
	if err := runner.SSH(ctx, taskName, "chmod +x /tmp/run-claude.sh"); err != nil {
		return fmt.Errorf("making run script executable: %w", err)
	}

	tw := newTranscriptWriter(os.Stdout, verbose)
	if err := runner.SSHProxyOutput(ctx, taskName, tw, "/tmp/run-claude.sh"); err != nil {
		slog.Error("Claude exited with error", "task", taskName, "error", err)
		// Don't return error - still want to archive results
	}

	slog.Info("Claude finished", "task", taskName)
	slog.Info("Task completed", "task", taskName)
	return nil
}

func setupSandbox(ctx context.Context, runner *gjoll.Runner, taskName string) error {
	// Configure git
	if err := runner.SSH(ctx, taskName, "git config --global user.name Drellabot"); err != nil {
		return fmt.Errorf("git config user.name: %w", err)
	}
	if err := runner.SSH(ctx, taskName, "git config --global user.email imagebuilder-bots+drella@redhat.com"); err != nil {
		return fmt.Errorf("git config user.email: %w", err)
	}

	// Initialize project repo
	if err := runner.SSH(ctx, taskName, "mkdir -p ~/project && cd ~/project && git init"); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	// Write CLAUDE.md to a temp file and copy it to the sandbox
	tmpFile, err := os.CreateTemp("", "prompt-*.md")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(promptContent); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing prompt: %w", err)
	}
	tmpFile.Close()

	if err := runner.Cp(ctx, taskName, tmpFile.Name(), ":~/project/CLAUDE.md"); err != nil {
		return fmt.Errorf("copying CLAUDE.md: %w", err)
	}

	// Initial commit
	if err := runner.SSH(ctx, taskName, "cd ~/project && git add -A && git commit -m 'Initial setup'"); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}

	// Register MCP server with Claude
	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", mcpserver.MCPPort)
	if err := runner.SSH(ctx, taskName, fmt.Sprintf("claude mcp add --transport http orchestrator %s --scope user", mcpURL)); err != nil {
		return fmt.Errorf("registering MCP server: %w", err)
	}

	return nil
}
