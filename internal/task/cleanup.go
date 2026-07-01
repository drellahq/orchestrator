package task

import (
	"context"
	"fmt"
	"strings"

	"github.com/drellahq/orchestrator/internal/config"
	"github.com/drellahq/orchestrator/internal/sandbox"
)

// CleanupOpts controls manual sandbox cleanup behavior.
type CleanupOpts struct {
	Force  bool
	DryRun bool
}

// sandboxDestroyer destroys a sandbox; replaced in tests.
var sandboxDestroyer = destroySandbox

// DestroySandbox tears down the named sandbox using cfg.sandbox_backend.
func DestroySandbox(ctx context.Context, cfg *config.Config, taskName string) error {
	return sandboxDestroyer(ctx, cfg, taskName)
}

func destroySandbox(ctx context.Context, cfg *config.Config, taskName string) error {
	runner, err := sandbox.NewFromConfig(
		cfg.SandboxBackend,
		cfg.GjollEnv,
		cfg.PodmanImage,
		cfg.AnthropicKeyPath(),
		0,
		"",
		nil,
	)
	if err != nil {
		return fmt.Errorf("creating sandbox runner: %w", err)
	}
	if err := runner.Down(ctx, taskName); err != nil {
		if isSandboxNotFound(err) {
			return nil
		}
		return fmt.Errorf("destroying sandbox: %w", err)
	}
	return nil
}

// ValidateCleanup checks whether a task is eligible for sandbox cleanup.
func ValidateCleanup(state *State, opts CleanupOpts) error {
	if opts.Force {
		return nil
	}
	if state.Status == StatusInProgress {
		return fmt.Errorf("task %q is in_progress; use --force to destroy anyway", state.Name)
	}
	if state.HasOpenPRs() {
		return fmt.Errorf("task %q has open PRs; use --force to destroy anyway", state.Name)
	}
	return nil
}

// CleanupTaskSandbox destroys the sandbox and updates task directory bookkeeping.
func CleanupTaskSandbox(ctx context.Context, cfg *config.Config, outputDir, taskName string, opts CleanupOpts) error {
	td, err := Open(outputDir, taskName)
	if err != nil {
		return err
	}
	state, err := td.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	if err := ValidateCleanup(state, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}

	if err := DestroySandbox(ctx, cfg, taskName); err != nil {
		return err
	}
	if !state.SandboxDestroyed {
		if err := td.SetSandboxDestroyed(); err != nil {
			return fmt.Errorf("marking sandbox destroyed: %w", err)
		}
	}
	if err := td.RemoveRepo(); err != nil {
		return fmt.Errorf("removing repo directory: %w", err)
	}
	return nil
}

func isSandboxNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "no container with name or id")
}
