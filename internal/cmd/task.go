package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/drellahq/orchestrator/internal/agent"
	"github.com/drellahq/orchestrator/internal/config"
	gh "github.com/drellahq/orchestrator/internal/github"
	"github.com/drellahq/orchestrator/internal/issueattachments"
	"github.com/drellahq/orchestrator/internal/logging"
	mcpserver "github.com/drellahq/orchestrator/internal/mcp"
	"github.com/drellahq/orchestrator/internal/profile"
	"github.com/drellahq/orchestrator/internal/prompts"
	"github.com/drellahq/orchestrator/internal/rhel"
	"github.com/drellahq/orchestrator/internal/sandbox"
	"github.com/drellahq/orchestrator/internal/task"
	"github.com/spf13/cobra"
)

var author string
var profileName string
var profileVars []string
var sourceRepo string
var sourceIssue int
var taskLabels []string
var agentBackendFlag string

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
	taskNewCmd.Flags().StringVar(&profileName, "profile", "", "profile to apply to the sandbox (e.g. \"code-review\")")
	taskNewCmd.Flags().StringSliceVar(&profileVars, "var", nil, "profile variables as KEY=VALUE (e.g. --var PROFILE_PR=42)")
	taskNewCmd.Flags().StringVar(&sourceRepo, "source-repo", "", "tasks-repo the task was spawned from (e.g. myorg/tasks)")
	taskNewCmd.Flags().IntVar(&sourceIssue, "source-issue", 0, "GitHub issue number in the tasks-repo")
	taskNewCmd.Flags().StringSliceVar(&taskLabels, "label", nil, "GitHub issue labels (e.g. --label rhel)")
	taskNewCmd.Flags().StringVar(&agentBackendFlag, "agent-backend", "", "override agent backend for this task (e.g. \"opencode\")")
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

	if sourceRepo != "" && sourceIssue > 0 {
		if err := taskDir.SaveSource(sourceRepo, sourceIssue); err != nil {
			return fmt.Errorf("saving source issue: %w", err)
		}
	}

	return executeTask(ctx, taskName, taskDescription, taskDir, cfg, ghRunner, false, author, profileName, profileVars, taskLabels, agentBackendFlag)
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

	return executeTask(ctx, taskName, taskDescription, taskDir, cfg, ghRunner, true, state.Author, "", nil, nil, "")
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

func createSandboxRunner(cfg *config.Config, backend agent.Backend) (sandbox.Runner, error) {
	slog.Info("Creating sandbox runner", "backend", cfg.SandboxBackend, "agent", backend.Name())
	return sandbox.NewFromConfig(cfg.SandboxBackend, cfg.GjollEnv, cfg.PodmanImage, cfg.AnthropicKeyFile, mcpserver.MCPRemotePort, backend.InstallCmd())
}

func executeTask(ctx context.Context, taskName, taskDescription string, taskDir *task.Dir, cfg *config.Config, ghRunner *gh.Runner, continueSession bool, author string, profileName string, profileVars []string, labels []string, agentBackendOverride string) error {
	backendName := cfg.AgentBackend
	if agentBackendOverride != "" {
		backendName = agentBackendOverride
	}
	backend, err := agent.New(backendName)
	if err != nil {
		return fmt.Errorf("creating agent backend: %w", err)
	}

	runner, err := createSandboxRunner(cfg, backend)
	if err != nil {
		return fmt.Errorf("creating sandbox runner: %w", err)
	}

	logger := slog.Default()

	// Start MCP server
	mcpSrv := mcpserver.New(logger, taskName, taskDir, runner, ghRunner, cfg.AllowedRepos, author, cfg.Daemon.TasksRepo, cfg.BaseURL)
	if err := mcpSrv.Start(); err != nil {
		return fmt.Errorf("starting MCP server: %w", err)
	}
	defer func() { _ = mcpSrv.Stop(context.Background()) }()

	mcpPort := mcpSrv.Port()
	mcpTunnel := fmt.Sprintf("%d:localhost:%d", mcpserver.MCPRemotePort, mcpPort)
	slog.Debug("MCP server started", "task", taskName, "port", mcpPort)

	var attachmentFiles []issueattachments.DownloadedFile
	if !continueSession {
		repo, num, ok := resolveTaskSource(taskDir, sourceRepo, sourceIssue, cfg.Daemon.TasksRepo)
		if !ok {
			repo, num = "", 0
		}
		files, err := issueattachments.Sync(ctx, ghRunner, taskDescription, repo, num, taskDir.AttachmentsPath())
		if err != nil {
			slog.Warn("Failed to sync issue attachments", "repo", repo, "issue", num, "error", err)
		} else {
			attachmentFiles = files
		}
	}

	if continueSession {
		slog.Info("Resuming sandbox", "task", taskName)
		if err := runner.Start(ctx, taskName); err != nil {
			return fmt.Errorf("resuming sandbox: %w", err)
		}
	} else {
		if hasLabel(labels, "rhel") {
			if err := setupRHELSubscription(ctx, taskName, taskDir); err != nil {
				return fmt.Errorf("RHEL subscription setup failed (required by 'rhel' label): %w", err)
			}
		}

		tfPath, err := filepath.Abs(cfg.GjollEnv)
		if err != nil {
			return fmt.Errorf("resolving tf path: %w", err)
		}

		slog.Info("Provisioning sandbox", "task", taskName)
		if err := runner.Up(ctx, taskName, tfPath); err != nil {
			return fmt.Errorf("provisioning sandbox: %w", err)
		}
	}

	home := runner.UserHome()

	// After successful provisioning, ensure cleanup on exit
	defer func() {
		slog.Debug("Copying transcript", "task", taskName)
		if copyErr := runner.Cp(context.Background(), taskName, taskName+":"+home+"/transcript.jsonl", taskDir.TranscriptPath()); copyErr != nil {
			slog.Warn("Failed to copy transcript", "task", taskName, "error", copyErr)
		}

		slog.Debug("Copying conversations", "task", taskName)
		if copyErr := runner.Cp(context.Background(), taskName, taskName+":"+home+"/"+backend.ConversationDir()+"/", taskDir.ConversationsPath()); copyErr != nil {
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

		cleanupRHELSubscription(context.Background(), taskName, taskDir)
	}()

	slog.Info("Sandbox provisioned", "task", taskName)

	if err := taskDir.SetStatus(task.StatusInProgress); err != nil {
		slog.Warn("Failed to set status to in_progress", "task", taskName, "error", err)
	}

	if !continueSession {
		if err := setupSandbox(ctx, runner, backend, taskName, taskDir, cfg, profileName, profileVars); err != nil {
			return fmt.Errorf("setting up sandbox: %w", err)
		}
		slog.Debug("Sandbox setup complete", "task", taskName)

		if err := copyAttachmentsToSandbox(ctx, runner, taskName, attachmentFiles); err != nil {
			return fmt.Errorf("copying attachments to sandbox: %w", err)
		}
	}

	taskDescription += issueattachments.Manifest(attachmentFiles)

	slog.Info("Running agent", "task", taskName, "agent", backend.Name())
	systemPromptFile := home + "/system-prompt.md"
	if backend.Name() == "opencode" {
		systemPromptFile = ""
	}
	runScript := backend.BuildRunScript(taskDescription, continueSession, systemPromptFile, cfg.Agent.MaxBudgetUSD)

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

	runScriptPath := home + "/run-claude.sh"
	if err := runner.Cp(ctx, taskName, tmpRun.Name(), taskName+":"+runScriptPath); err != nil {
		return fmt.Errorf("copying run script: %w", err)
	}
	if err := runner.SSH(ctx, taskName, "bash", "-c", "chmod a+rx "+runScriptPath); err != nil {
		return fmt.Errorf("making run script executable: %w", err)
	}

	sshOpts := &sandbox.SSHOpts{
		Proxy:          true,
		ReverseTunnels: []string{mcpTunnel},
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

	tw := newTranscriptWriter(os.Stdout, verbose, backend)
	w := io.MultiWriter(tw, transcriptFile)
	var agentErr error
	if agentErr = runner.SSHProxyOutput(ctx, taskName, w, sshOpts, "bash", "-c", runner.AsUser(runScriptPath)); agentErr != nil {
		slog.Error("Agent exited with error", "task", taskName, "agent", backend.Name(), "error", agentErr)
	}

	slog.Info("Agent finished", "task", taskName, "agent", backend.Name())

	if err := taskDir.TouchUpdatedAt(time.Now()); err != nil {
		slog.Warn("Failed to update updated_at", "task", taskName, "error", err)
	}

	if usage, err := task.ParseTranscriptUsage(taskDir.TranscriptPath(), backend); err != nil {
		slog.Warn("Failed to parse transcript usage", "task", taskName, "error", err)
	} else if err := taskDir.SaveUsage(usage); err != nil {
		slog.Warn("Failed to save usage", "task", taskName, "error", err)
	}

	state, err := taskDir.LoadState()
	if err != nil {
		slog.Warn("Failed to load state for status update", "task", taskName, "error", err)
	} else {
		finalStatus := task.StatusDone
		if state.HasOpenPRs() {
			finalStatus = task.StatusWaiting
		} else if agentErr != nil {
			finalStatus = task.StatusError
		}
		if err := taskDir.SetStatus(finalStatus); err != nil {
			slog.Warn("Failed to set final status", "task", taskName, "error", err)
		}
	}

	slog.Info("Task completed", "task", taskName)
	return nil
}

func setupSandbox(ctx context.Context, runner sandbox.Runner, backend agent.Backend, taskName string, taskDir *task.Dir, cfg *config.Config, profileName string, profileVars []string) error {
	// Always: configure git (run as sandbox user)
	if err := runner.SSH(ctx, taskName, "bash", "-c", runner.AsUser("git config --global user.name Drellabot")); err != nil {
		return fmt.Errorf("git config user.name: %w", err)
	}
	if err := runner.SSH(ctx, taskName, "bash", "-c", runner.AsUser("git config --global user.email imagebuilder-bots+drella@redhat.com")); err != nil {
		return fmt.Errorf("git config user.email: %w", err)
	}

	// Always: register orchestrator MCP server
	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", mcpserver.MCPRemotePort)
	mcpCmd := backend.MCPAddCmd("orchestrator", "http", mcpURL, "user")
	if err := runner.SSH(ctx, taskName, "bash", "-c", runner.AsUser(mcpCmd)); err != nil {
		return fmt.Errorf("registering MCP server: %w", err)
	}

	if profileName != "" {
		return setupSandboxWithProfile(ctx, runner, backend, taskName, taskDir, cfg, profileName, profileVars)
	}
	return setupSandboxDefault(ctx, runner, backend, taskName)
}

// setupSandboxWithProfile applies a profile's configuration to the sandbox.
func setupSandboxWithProfile(ctx context.Context, runner sandbox.Runner, backend agent.Backend, taskName string, taskDir *task.Dir, cfg *config.Config, profileName string, profileVars []string) error {
	profileSource, cleanup, err := resolveProfileSource(ctx, cfg)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	p, err := profile.Load(profileSource, profileName)
	if err != nil {
		return fmt.Errorf("loading profile: %w", err)
	}

	slog.Info("Applying profile", "profile", profileName, "task", taskName)

	vars := parseVarFlags(profileVars)
	if err := profile.Apply(ctx, p, runner, backend, taskName, taskDir.Path(), prompts.Base, vars); err != nil {
		return fmt.Errorf("applying profile: %w", err)
	}

	return writeSystemPrompt(ctx, runner, backend, taskName)
}

// setupSandboxDefault preserves the existing behavior when no profile is specified.
func setupSandboxDefault(ctx context.Context, runner sandbox.Runner, backend agent.Backend, taskName string) error {
	return writeSystemPrompt(ctx, runner, backend, taskName)
}

// writeSystemPrompt writes the on_init system prompt into the sandbox.
// For claude-code: writes ~/system-prompt.md (used via --append-system-prompt-file).
// For opencode: appends to ~/.claude/CLAUDE.md (OpenCode reads this natively
// and has no --append-system-prompt-file equivalent).
func writeSystemPrompt(ctx context.Context, runner sandbox.Runner, backend agent.Backend, taskName string) error {
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

	home := runner.UserHome()

	if backend.Name() == "opencode" {
		// OpenCode lacks --append-system-prompt-file; append on_init to
		// the CLAUDE.md that OpenCode reads natively.
		configDir := backend.ConfigDir()
		runner.SSH(ctx, taskName, "bash", "-c", runner.AsUser("mkdir -p ~/"+configDir))
		claudemdDest := home + "/" + configDir + "/CLAUDE.md"
		if err := runner.Cp(ctx, taskName, tmpFile.Name(), taskName+":"+home+"/system-prompt-tmp.md"); err != nil {
			return fmt.Errorf("copying system prompt: %w", err)
		}
		if err := runner.SSH(ctx, taskName, "bash", "-c", runner.AsUser(fmt.Sprintf("cat %s/system-prompt-tmp.md >> %s && rm %s/system-prompt-tmp.md", home, claudemdDest, home))); err != nil {
			return fmt.Errorf("appending system prompt to CLAUDE.md: %w", err)
		}
		return nil
	}

	promptDest := home + "/system-prompt.md"
	if err := runner.Cp(ctx, taskName, tmpFile.Name(), taskName+":"+promptDest); err != nil {
		return fmt.Errorf("copying system prompt: %w", err)
	}
	if err := runner.SSH(ctx, taskName, "bash", "-c", "chmod a+r "+promptDest); err != nil {
		return fmt.Errorf("fixing system prompt permissions: %w", err)
	}

	return nil
}

// parseVarFlags parses --var KEY=VALUE flags into a map.
func parseVarFlags(flags []string) map[string]string {
	if len(flags) == 0 {
		return nil
	}
	vars := make(map[string]string, len(flags))
	for _, f := range flags {
		k, v, ok := strings.Cut(f, "=")
		if ok {
			vars[k] = v
		}
	}
	return vars
}

// resolveProfileSource returns the directory containing profiles.
// If profiles_dir is set, it's used directly. Otherwise, profiles_repo
// is shallow-cloned to a temp directory (returned cleanup removes it).
func resolveProfileSource(ctx context.Context, cfg *config.Config) (dir string, cleanup func(), err error) {
	if cfg.ProfilesDir != "" {
		return cfg.ProfilesDir, nil, nil
	}

	if cfg.ProfilesRepo == "" {
		return "", nil, fmt.Errorf("--profile requires profiles_repo or profiles_dir in config")
	}

	tmpDir, err := os.MkdirTemp("", "profiles-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp dir for profiles: %w", err)
	}

	cloneDir := filepath.Join(tmpDir, "profiles")
	slog.Debug("Cloning profiles repo", "repo", cfg.ProfilesRepo, "dest", cloneDir)

	cmd := exec.CommandContext(ctx, "gh", "repo", "clone", cfg.ProfilesRepo, cloneDir, "--", "--depth=1")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("cloning profiles repo %q: %w", cfg.ProfilesRepo, err)
	}

	return cloneDir, func() { os.RemoveAll(tmpDir) }, nil
}

func resolveTaskSource(taskDir *task.Dir, explicitRepo string, explicitIssue int, defaultTasksRepo string) (repo string, issueNum int, ok bool) {
	if explicitRepo != "" && explicitIssue > 0 {
		return explicitRepo, explicitIssue, true
	}
	state, err := taskDir.LoadState()
	if err != nil || state.Source == nil || state.Source.IssueNumber == 0 {
		return "", 0, false
	}
	repo = state.Source.TasksRepo
	if repo == "" {
		repo = defaultTasksRepo
	}
	if repo == "" {
		return "", 0, false
	}
	return repo, state.Source.IssueNumber, true
}

func hasLabel(labels []string, name string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, name) {
			return true
		}
	}
	return false
}

// setupRHELSubscription creates an activation key via the Red Hat API and sets
// TF_VAR_ environment variables so the sandbox init_script can register with
// subscription-manager. The env vars are inherited by the gjoll subprocess.
// The activation key is also persisted in the task's state_secrets.json so it
// can be recovered on orchestrator restart.
func setupRHELSubscription(ctx context.Context, taskName string, taskDir *task.Dir) error {
	clientID := os.Getenv("LIGHTSPEED_CLIENT_ID")
	clientSecret := os.Getenv("LIGHTSPEED_CLIENT_SECRET")
	orgID := os.Getenv("LIGHTSPEED_ORG_ID")

	if clientID == "" || clientSecret == "" || orgID == "" {
		return fmt.Errorf("LIGHTSPEED_CLIENT_ID, LIGHTSPEED_CLIENT_SECRET, and LIGHTSPEED_ORG_ID must be set")
	}

	slog.Info("Creating RHEL activation key", "task", taskName)

	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("generating random suffix: %w", err)
	}
	akName := fmt.Sprintf("orchestrator-%s-%s", taskName, hex.EncodeToString(suffix[:]))

	client := rhel.NewClient(clientID, clientSecret)
	keyName, err := client.CreateActivationKey(ctx, akName)
	if err != nil {
		return fmt.Errorf("creating activation key: %w", err)
	}

	slog.Info("RHEL activation key created", "task", taskName, "key", keyName)

	if err := taskDir.SaveSecret("rhel_activation_key", keyName); err != nil {
		slog.Warn("Failed to persist RHEL activation key to state_secrets.json", "task", taskName, "error", err)
	}

	os.Setenv("TF_VAR_rhel_org_id", orgID)
	os.Setenv("TF_VAR_rhel_activation_key", keyName)

	return nil
}

// cleanupRHELSubscription deletes the RHEL activation key created for the task.
// It reads the key name from state_secrets.json and removes it from the RHSM API.
func cleanupRHELSubscription(ctx context.Context, taskName string, taskDir *task.Dir) {
	secrets, err := taskDir.LoadSecrets()
	if err != nil || len(secrets) == 0 {
		return
	}

	keyName, ok := secrets["rhel_activation_key"]
	if !ok || keyName == "" {
		return
	}

	clientID := os.Getenv("LIGHTSPEED_CLIENT_ID")
	clientSecret := os.Getenv("LIGHTSPEED_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		slog.Warn("Cannot clean up RHEL activation key: missing LIGHTSPEED credentials", "task", taskName, "key", keyName)
		return
	}

	slog.Info("Deleting RHEL activation key", "task", taskName, "key", keyName)
	client := rhel.NewClient(clientID, clientSecret)
	if err := client.DeleteActivationKey(ctx, keyName); err != nil {
		slog.Warn("Failed to delete RHEL activation key", "task", taskName, "key", keyName, "error", err)
		return
	}
	slog.Info("RHEL activation key deleted", "task", taskName, "key", keyName)
}

func copyAttachmentsToSandbox(ctx context.Context, runner sandbox.Runner, taskName string, files []issueattachments.DownloadedFile) error {
	if len(files) == 0 {
		return nil
	}
	if err := runner.SSH(ctx, taskName, "mkdir -p ~/attachments"); err != nil {
		return fmt.Errorf("mkdir ~/attachments: %w", err)
	}
	for _, f := range files {
		dest := ":~/attachments/" + f.Filename
		if err := runner.Cp(ctx, taskName, f.LocalPath, dest); err != nil {
			return fmt.Errorf("copying %q: %w", f.Filename, err)
		}
		slog.Debug("Copied attachment to sandbox", "task", taskName, "file", f.Filename)
	}
	return nil
}
