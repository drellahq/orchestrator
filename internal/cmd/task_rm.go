package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/drellahq/orchestrator/internal/task"
	"github.com/spf13/cobra"
)

var taskRmForce bool
var taskRmDryRun bool
var taskRmYes bool

var taskRmCmd = &cobra.Command{
	Use:     "rm <task-name>",
	Aliases: []string{"wipe"},
	Short:   "Destroy a task sandbox and remove its repo directory",
	Args:    cobra.ExactArgs(1),
	RunE:    runTaskRm,
}

func init() {
	taskRmCmd.Flags().BoolVar(&taskRmForce, "force", false, "destroy even if task is in_progress or has open PRs")
	taskRmCmd.Flags().BoolVar(&taskRmDryRun, "dry-run", false, "show what would happen without destroying")
	taskRmCmd.Flags().BoolVarP(&taskRmYes, "yes", "y", false, "skip confirmation prompt")
	taskCmd.AddCommand(taskRmCmd)
}

func runTaskRm(cmd *cobra.Command, args []string) error {
	taskName := args[0]

	cfg, err := loadConfigAndSetupLogging()
	if err != nil {
		return err
	}

	td, err := task.Open(cfg.OutputDir, taskName)
	if err != nil {
		return err
	}
	state, err := td.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	if err := task.ValidateCleanup(state, task.CleanupOpts{Force: taskRmForce}); err != nil {
		return err
	}

	action := fmt.Sprintf("Destroy sandbox %q (%s backend)", taskName, cfg.SandboxBackend)
	if taskRmDryRun {
		fmt.Printf("dry-run: would %s\n", strings.ToLower(action))
		return nil
	}

	if !taskRmYes {
		fmt.Printf("%s and remove repo/ for task %q? [y/N] ", action, taskName)
		reader := bufio.NewReader(os.Stdin)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	opts := task.CleanupOpts{Force: taskRmForce}
	if err := task.CleanupTaskSandbox(context.Background(), cfg, cfg.OutputDir, taskName, opts); err != nil {
		return err
	}

	fmt.Printf("Destroyed sandbox for task %q\n", taskName)
	return nil
}
