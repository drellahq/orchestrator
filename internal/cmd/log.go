package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/drellahq/orchestrator/internal/agent"
	"github.com/drellahq/orchestrator/internal/config"
	"github.com/drellahq/orchestrator/internal/sandbox"
	"github.com/drellahq/orchestrator/internal/task"
	"github.com/spf13/cobra"
)

var followFlag bool

var logCmd = &cobra.Command{
	Use:   "log <task-name>",
	Short: "Show agent transcript for a task",
	Long: `Shows the streaming JSON transcript from an agent session.

Without -f, reads the local transcript from the task directory (after the
task has completed and the transcript has been copied back).

With -f/--follow, tails the live transcript from the running sandbox VM
via SSH, formatted for human readability.

Use -v to also show the model's internal reasoning (thinking blocks).

Examples:
  orchestrator log my-task          Show completed task transcript
  orchestrator log -v my-task       Include model reasoning
  orchestrator log -f my-task       Follow live transcript`,
	Args: cobra.ExactArgs(1),
	RunE: runLog,
}

func init() {
	logCmd.Flags().BoolVarP(&followFlag, "follow", "f", false, "follow live transcript via SSH")
}

func runLog(cmd *cobra.Command, args []string) error {
	taskName := args[0]

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	opts := cfg.AgentOptions()
	backend, err := agent.New(cfg.AgentBackend, &opts)
	if err != nil {
		return fmt.Errorf("creating agent backend: %w", err)
	}

	if followFlag {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()

		runner, err := sandbox.NewFromConfig(cfg.SandboxBackend, cfg.GjollEnv, cfg.PodmanImage, cfg.AnthropicKeyFile, 19090, backend.InstallCmd(), nil)
		if err != nil {
			return fmt.Errorf("creating sandbox runner: %w", err)
		}
		tw := newTranscriptWriter(os.Stdout, verbose, backend)
		home := runner.UserHome()
		sshOpts := &sandbox.SSHOpts{Proxy: cfg.SandboxBackend == "gjoll"}
		return runner.SSHProxyOutput(ctx, taskName, tw, sshOpts, "bash", "-c", "tail -f "+home+"/transcript.jsonl")
	}

	transcriptPath := task.TranscriptPathFor(cfg.OutputDir, taskName)
	f, err := os.Open(transcriptPath)
	if err != nil {
		return fmt.Errorf("opening transcript: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		formatted := backend.FormatTranscriptLine(scanner.Bytes(), verbose)
		if formatted != "" {
			fmt.Print(formatted)
		}
	}
	return scanner.Err()
}

// transcriptWriter is an io.Writer that buffers input until complete JSONL
// lines are available, then formats each line for human readability.
type transcriptWriter struct {
	w       io.Writer
	buf     []byte
	verbose bool
	backend agent.Backend
}

func newTranscriptWriter(w io.Writer, verbose bool, backend agent.Backend) *transcriptWriter {
	return &transcriptWriter{w: w, verbose: verbose, backend: backend}
}

func (tw *transcriptWriter) Write(p []byte) (int, error) {
	tw.buf = append(tw.buf, p...)
	for {
		idx := bytes.IndexByte(tw.buf, '\n')
		if idx < 0 {
			break
		}
		line := tw.buf[:idx]
		tw.buf = tw.buf[idx+1:]
		formatted := tw.backend.FormatTranscriptLine(line, tw.verbose)
		if formatted != "" {
			if _, err := io.WriteString(tw.w, formatted); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}
