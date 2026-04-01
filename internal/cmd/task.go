package cmd

import (
	"context"
	"fmt"
	"io"
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
	"github.com/drellabot/orchestrator/internal/pipeline"
	"github.com/drellabot/orchestrator/internal/prompts"
	"github.com/drellabot/orchestrator/internal/task"
	"github.com/spf13/cobra"
)

var author string

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

var taskContinueCmd = &cobra.Command{
	Use:   "continue <task-name> <task-description...>",
	Short: "Continue a stopped task with a new prompt",
	Long: `Resumes a stopped sandbox VM, starts an MCP server, and launches Claude
with --continue to resume the previous conversation with a new prompt.`,
	Args: cobra.MinimumNArgs(2),
	RunE: continueTask,
}

func init() {
	taskNewCmd.Flags().StringVar(&author, "author", "", "co-author to add to PR commits (e.g. \"Jane Doe <jane@example.com>\")")
	taskCmd.AddCommand(taskNewCmd)
	taskCmd.AddCommand(taskContinueCmd)
	taskCmd.AddCommand(taskWatchCmd)
}

func runTask(cmd *cobra.Command, args []string) error {
	taskName := args[0]
	taskDescription := strings.Join(args[1:], " ")

	cfg, err := loadConfigAndSetupLogging()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	ghRunner := logPreflightWarnings(ctx, cfg)

	slog.Info("Task started", "task", taskName)

	taskDir, err := task.Create(cfg.OutputDir, taskName)
	if err != nil {
		return fmt.Errorf("creating task directory: %w", err)
	}

	if err := taskDir.SaveMetadata(taskName, taskDescription, author, time.Now()); err != nil {
		return fmt.Errorf("saving metadata: %w", err)
	}

	return executeTask(ctx, taskName, taskDescription, taskDir, cfg, ghRunner, false, author)
}

func continueTask(cmd *cobra.Command, args []string) error {
	taskName := args[0]
	taskDescription := strings.Join(args[1:], " ")

	cfg, err := loadConfigAndSetupLogging()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	ghRunner := logPreflightWarnings(ctx, cfg)

	slog.Info("Task continuing", "task", taskName)

	taskDir, err := task.Open(cfg.OutputDir, taskName)
	if err != nil {
		return fmt.Errorf("opening task directory: %w", err)
	}

	state, err := taskDir.LoadState()
	if err != nil {
		return fmt.Errorf("loading task state: %w", err)
	}

	return executeTask(ctx, taskName, taskDescription, taskDir, cfg, ghRunner, true, state.Author)
}

func loadConfigAndSetupLogging() (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	logger := logging.SetupLogger(cfg.SlackWebhook, verbose)
	slog.SetDefault(logger)

	return cfg, nil
}

func logPreflightWarnings(ctx context.Context, cfg *config.Config) *gh.Runner {
	if len(cfg.AllowedRepos) == 0 {
		slog.Warn("allowed_repos is empty; open_pr, update_pr, and comment_on_pr tools will not be available")
	}

	ghRunner := gh.New("")
	if _, err := ghRunner.AuthenticatedUser(ctx); err != nil {
		slog.Warn("GitHub CLI not authenticated; open_pr, update_pr, and comment_on_pr tools will not be available", "error", err)
	}

	return ghRunner
}

func executeTask(ctx context.Context, taskName, taskDescription string, taskDir *task.Dir, cfg *config.Config, ghRunner *gh.Runner, continueSession bool, author string) error {
	runner := gjoll.New("")

	logger := slog.Default()

	// Start MCP server
	mcpSrv := mcpserver.New(logger, taskName, taskDir, runner, ghRunner, cfg.AllowedRepos, author)
	if err := mcpSrv.Start(); err != nil {
		return fmt.Errorf("starting MCP server: %w", err)
	}
	defer func() { _ = mcpSrv.Stop(context.Background()) }()

	mcpPort := mcpSrv.Port()
	mcpTunnel := fmt.Sprintf("%d:localhost:%d", mcpserver.MCPRemotePort, mcpPort)
	slog.Debug("MCP server started", "task", taskName, "port", mcpPort)

	if continueSession {
		slog.Info("Resuming sandbox", "task", taskName)
		if err := runner.Start(ctx, taskName); err != nil {
			return fmt.Errorf("resuming sandbox: %w", err)
		}
	} else {
		tfPath, err := filepath.Abs(cfg.GjollEnv)
		if err != nil {
			return fmt.Errorf("resolving tf path: %w", err)
		}

		slog.Info("Provisioning sandbox", "task", taskName)
		if err := runner.Up(ctx, taskName, tfPath); err != nil {
			return fmt.Errorf("provisioning sandbox: %w", err)
		}
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

		// Flush filesystem writes before stopping — the libvirt provider
		// only waits 5 seconds for graceful ACPI shutdown before force-
		// destroying the VM, which can lose unflushed data.
		slog.Debug("Syncing filesystem", "task", taskName)
		_ = runner.SSH(context.Background(), taskName, "sync")

		slog.Debug("Stopping sandbox", "task", taskName)
		if stopErr := runner.Stop(context.Background(), taskName); stopErr != nil {
			slog.Warn("Failed to stop sandbox", "task", taskName, "error", stopErr)
		}
	}()

	slog.Info("Sandbox provisioned", "task", taskName)

	if !continueSession {
		// For continue sessions, sandbox is already set up; we only need
		// to write the system prompt for the current pipeline step below.
		if err := setupSandboxBase(ctx, runner, taskName); err != nil {
			return fmt.Errorf("setting up sandbox: %w", err)
		}
		slog.Debug("Sandbox setup complete", "task", taskName)
	}

	sshOpts := &gjoll.SSHOpts{
		Proxy:          true,
		ReverseTunnels: []string{mcpTunnel},
	}

	// For continue sessions, run a single Claude session (no pipeline).
	if continueSession {
		return runSingleSession(ctx, runner, taskName, taskDescription, taskDir, sshOpts, true)
	}

	// Load the pipeline and execute it.
	steps := cfg.Pipeline("")
	multiStep := pipeline.IsMultiStep(steps)

	if !multiStep {
		// Single-step pipeline: backward-compatible behavior.
		if err := writeSystemPrompt(ctx, runner, taskName, cfg.AgentsDir, steps[0].Role); err != nil {
			return err
		}
		return runSingleSession(ctx, runner, taskName, taskDescription, taskDir, sshOpts, false)
	}

	// Multi-step pipeline execution.
	return executePipeline(ctx, runner, taskName, taskDescription, taskDir, cfg, ghRunner, sshOpts, steps)
}

// executePipeline runs a multi-step pipeline with iteration support.
//
// The pipeline assumes the last step is a reviewing role that produces a
// structured VERDICT (pass/fail). When the verdict is "fail", the pipeline
// re-runs all steps from the beginning with the reviewer's feedback
// appended to the first step's prompt. This loop repeats until the
// reviewer passes or max_iterations (configured on the last step) is
// reached.
func executePipeline(ctx context.Context, runner *gjoll.Runner, taskName, taskDescription string, taskDir *task.Dir, cfg *config.Config, ghRunner *gh.Runner, sshOpts *gjoll.SSHOpts, steps []config.PipelineStep) error {
	pipelineState := pipeline.NewState("default", steps)
	if err := taskDir.SavePipelineState(pipelineState); err != nil {
		return fmt.Errorf("saving pipeline state: %w", err)
	}

	// Track findings from the reviewing step (last step) across iterations
	// for the escalation comment if max iterations is reached.
	var reviewFindings []string

	// The iteration loop: all steps run in order. If the last step's
	// verdict is "fail", we loop back with its feedback.
	maxIter := steps[len(steps)-1].MaxIterations
	for iteration := 1; iteration <= maxIter; iteration++ {
		for stepIdx, step := range steps {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			slog.Info("Pipeline step", "task", taskName, "role", step.Role,
				"step", stepIdx+1, "of", len(steps), "iteration", iteration)

			// Update pipeline state.
			pipelineState.CurrentStep = stepIdx
			pipelineState.Iteration = iteration
			pipelineState.Steps[stepIdx].Status = "running"
			pipelineState.Steps[stepIdx].Iterations = iteration
			if err := taskDir.SavePipelineState(pipelineState); err != nil {
				slog.Warn("Failed to save pipeline state", "error", err)
			}

			// Write the system prompt for this role.
			if err := writeSystemPrompt(ctx, runner, taskName, cfg.AgentsDir, step.Role); err != nil {
				return err
			}

			// Build the user prompt.
			var userPrompt string
			if stepIdx == 0 && iteration == 1 {
				// First producer run: just the task description.
				userPrompt = taskDescription
			} else if stepIdx == 0 && iteration > 1 {
				// First step re-run after the reviewing step's failure.
				lastFindings := reviewFindings[len(reviewFindings)-1]
				userPrompt = pipeline.BuildFeedbackPrompt(taskDescription, lastFindings)
			} else {
				// Non-first step (e.g. validator): build handoff prompt.
				diff, err := getDiff(ctx, runner, taskName)
				if err != nil {
					slog.Warn("Failed to get diff for handoff", "error", err)
				}
				userPrompt = pipeline.BuildHandoffPrompt(taskDescription, diff, "", step.Handoff)
			}

			// Run Claude for this step.
			transcriptFile := taskDir.IterationTranscriptPath(step.Role, iteration, true)
			if err := runClaudeSession(ctx, runner, taskName, userPrompt, transcriptFile, sshOpts); err != nil {
				slog.Error("Claude exited with error", "task", taskName, "role", step.Role, "error", err)
			}

			pipelineState.Steps[stepIdx].Status = "completed"

			// The last step is expected to produce a VERDICT line.
			if stepIdx == len(steps)-1 {
				transcript, err := os.ReadFile(transcriptFile)
				if err != nil {
					slog.Error("Failed to read transcript for verdict", "error", err)
					pipelineState.Steps[stepIdx].Verdict = pipeline.VerdictFail
					break
				}

				verdict, findings, err := pipeline.ParseVerdict(transcript)
				if err != nil {
					slog.Warn("Failed to parse verdict, treating as fail", "error", err)
					verdict = pipeline.VerdictFail
					findings = fmt.Sprintf("Could not parse verdict: %v", err)
				}

				pipelineState.Steps[stepIdx].Verdict = verdict
				slog.Info("Validator verdict", "task", taskName, "verdict", verdict, "iteration", iteration)

				if verdict == pipeline.VerdictPass {
					if err := taskDir.SavePipelineState(pipelineState); err != nil {
						slog.Warn("Failed to save pipeline state", "error", err)
					}
					// Pipeline succeeded — mark PR as ready.
					return finalizePipeline(ctx, taskDir, ghRunner, taskName, true, nil)
				}

				// Verdict is fail — record findings for potential escalation.
				reviewFindings = append(reviewFindings, findings)
			}

			if err := taskDir.SavePipelineState(pipelineState); err != nil {
				slog.Warn("Failed to save pipeline state", "error", err)
			}
		}
	}

	// Max iterations reached without passing — escalate.
	slog.Warn("Pipeline max iterations reached", "task", taskName, "iterations", maxIter)
	return finalizePipeline(ctx, taskDir, ghRunner, taskName, false, reviewFindings)
}

// finalizePipeline handles PR readiness signaling after pipeline completion.
func finalizePipeline(ctx context.Context, taskDir *task.Dir, ghRunner *gh.Runner, taskName string, passed bool, findings []string) error {
	if err := taskDir.TouchUpdatedAt(time.Now()); err != nil {
		slog.Warn("Failed to update updated_at", "task", taskName, "error", err)
	}

	state, err := taskDir.LoadState()
	if err != nil {
		slog.Warn("Failed to load state for PR finalization", "error", err)
		return nil
	}

	for _, pr := range state.Resources.GitHub.PRs {
		if pr.Closed {
			continue
		}

		if passed {
			slog.Info("Marking PR as ready", "task", taskName, "pr", pr.URL)
			if err := ghRunner.MarkPRReady(ctx, pr.URL); err != nil {
				slog.Warn("Failed to mark PR ready", "pr", pr.URL, "error", err)
			}
		} else {
			slog.Info("Adding needs-human-input label", "task", taskName, "pr", pr.URL)
			if err := ghRunner.AddLabelToPR(ctx, pr.URL, "needs-human-input"); err != nil {
				slog.Warn("Failed to add label", "pr", pr.URL, "error", err)
			}

			comment := pipeline.EscalationComment(len(findings), findings)
			if err := ghRunner.CommentOnPR(ctx, pr.URL, comment); err != nil {
				slog.Warn("Failed to post escalation comment", "pr", pr.URL, "error", err)
			}
		}
	}

	if passed {
		slog.Info("Pipeline completed successfully", "task", taskName)
	} else {
		slog.Info("Pipeline escalated to human review", "task", taskName)
	}
	return nil
}

// runSingleSession runs a single Claude session (used for single-step
// pipelines and continue sessions).
func runSingleSession(ctx context.Context, runner *gjoll.Runner, taskName, taskDescription string, taskDir *task.Dir, sshOpts *gjoll.SSHOpts, continueSession bool) error {
	slog.Info("Running Claude", "task", taskName)
	runScript := buildRunScript(taskDescription, continueSession)

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

	// Write the raw JSONL transcript to the host file in real-time so the
	// dashboard can poll it while the task is still running.
	var transcriptFlags int
	if continueSession {
		transcriptFlags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	} else {
		transcriptFlags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	transcriptFile, err := os.OpenFile(taskDir.TranscriptPath(), transcriptFlags, 0644)
	if err != nil {
		return fmt.Errorf("opening transcript file: %w", err)
	}
	defer transcriptFile.Close()

	tw := newTranscriptWriter(os.Stdout, verbose)
	w := io.MultiWriter(tw, transcriptFile)
	if err := runner.SSHProxyOutput(ctx, taskName, w, sshOpts, "/tmp/run-claude.sh"); err != nil {
		slog.Error("Claude exited with error", "task", taskName, "error", err)
		// Don't return error - still want to archive results
	}

	slog.Info("Claude finished", "task", taskName)

	if err := taskDir.TouchUpdatedAt(time.Now()); err != nil {
		slog.Warn("Failed to update updated_at", "task", taskName, "error", err)
	}

	slog.Info("Task completed", "task", taskName)
	return nil
}

// runClaudeSession runs a single Claude session for a pipeline step,
// writing output to the specified transcript file.
func runClaudeSession(ctx context.Context, runner *gjoll.Runner, taskName, userPrompt, transcriptPath string, sshOpts *gjoll.SSHOpts) error {
	runScript := buildRunScript(userPrompt, false)

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

	transcriptFile, err := os.OpenFile(transcriptPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("opening transcript file: %w", err)
	}
	defer transcriptFile.Close()

	tw := newTranscriptWriter(os.Stdout, verbose)
	w := io.MultiWriter(tw, transcriptFile)

	return runner.SSHProxyOutput(ctx, taskName, w, sshOpts, "/tmp/run-claude.sh")
}

// writeSystemPrompt writes the assembled system prompt for a role to the sandbox.
func writeSystemPrompt(ctx context.Context, runner *gjoll.Runner, taskName, agentsDir, role string) error {
	systemPrompt, err := pipeline.BuildAgentSystemPrompt(agentsDir, role)
	if err != nil {
		return fmt.Errorf("building system prompt for %s: %w", role, err)
	}

	tmpFile, err := os.CreateTemp("", "prompt-*.md")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(systemPrompt); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing prompt: %w", err)
	}
	tmpFile.Close()

	if err := runner.Cp(ctx, taskName, tmpFile.Name(), ":~/system-prompt.md"); err != nil {
		return fmt.Errorf("copying system prompt: %w", err)
	}
	return nil
}

// getDiff returns the git diff of uncommitted + committed changes in the sandbox.
func getDiff(ctx context.Context, runner *gjoll.Runner, taskName string) (string, error) {
	// Get diff of all committed changes vs the base branch.
	// We try common base branch names; the actual diff command will
	// succeed even if the range is empty.
	diff, err := runner.SSHOutput(ctx, taskName, "cd ~/project 2>/dev/null && git diff origin/main...HEAD 2>/dev/null || git diff HEAD~10 2>/dev/null || echo '(no diff available)'")
	if err != nil {
		return "", err
	}
	return diff, nil
}

func buildRunScript(taskDescription string, continueSession bool) string {
	escapedDesc := strings.ReplaceAll(taskDescription, "'", "'\\''")

	var claudeFlags string
	var teeFlag string
	if continueSession {
		claudeFlags = "--continue"
		teeFlag = "-a"
	}

	return fmt.Sprintf(`#!/bin/bash
source ~/.bashrc
stdbuf -oL claude --dangerously-skip-permissions -p --verbose \
  --output-format stream-json --append-system-prompt-file ~/system-prompt.md \
  %s '%s' \
  </dev/null | stdbuf -oL tee %s ~/transcript.jsonl
`, claudeFlags, escapedDesc, teeFlag)
}

// setupSandboxBase configures git and registers the MCP server in the sandbox.
// It does NOT write the system prompt — that is done per-pipeline-step.
func setupSandboxBase(ctx context.Context, runner *gjoll.Runner, taskName string) error {
	// Configure git
	if err := runner.SSH(ctx, taskName, "git config --global user.name Drellabot"); err != nil {
		return fmt.Errorf("git config user.name: %w", err)
	}
	if err := runner.SSH(ctx, taskName, "git config --global user.email imagebuilder-bots+drella@redhat.com"); err != nil {
		return fmt.Errorf("git config user.email: %w", err)
	}

	// Write a default system prompt (will be overwritten by pipeline steps).
	tmpFile, err := os.CreateTemp("", "prompt-*.md")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(prompts.OnInit); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing prompt: %w", err)
	}
	tmpFile.Close()

	if err := runner.Cp(ctx, taskName, tmpFile.Name(), ":~/system-prompt.md"); err != nil {
		return fmt.Errorf("copying system prompt: %w", err)
	}

	// Register MCP server with Claude
	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", mcpserver.MCPRemotePort)
	if err := runner.SSH(ctx, taskName, fmt.Sprintf("claude mcp add --transport http orchestrator %s --scope user", mcpURL)); err != nil {
		return fmt.Errorf("registering MCP server: %w", err)
	}

	return nil
}
