package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drellahq/orchestrator/internal/config"
	"github.com/drellahq/orchestrator/internal/task"
)

func TestParseVarFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  map[string]string
	}{
		{
			name:  "nil flags",
			flags: nil,
			want:  nil,
		},
		{
			name:  "empty flags",
			flags: []string{},
			want:  nil,
		},
		{
			name:  "single var",
			flags: []string{"PROFILE_PR=42"},
			want:  map[string]string{"PROFILE_PR": "42"},
		},
		{
			name:  "multiple vars",
			flags: []string{"PROFILE_REPO=org/repo", "PROFILE_PR=42"},
			want:  map[string]string{"PROFILE_REPO": "org/repo", "PROFILE_PR": "42"},
		},
		{
			name:  "value with equals sign",
			flags: []string{"KEY=a=b=c"},
			want:  map[string]string{"KEY": "a=b=c"},
		},
		{
			name:  "no equals sign is skipped",
			flags: []string{"NOEQUALS"},
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVarFlags(tt.flags)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parseVarFlags(%v) = %v, want nil", tt.flags, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseVarFlags(%v) has %d entries, want %d", tt.flags, len(got), len(tt.want))
			}
			for k, wantV := range tt.want {
				if got[k] != wantV {
					t.Errorf("parseVarFlags(%v)[%q] = %q, want %q", tt.flags, k, got[k], wantV)
				}
			}
		})
	}
}

func TestBuildRunScript(t *testing.T) {
	tests := []struct {
		name            string
		taskDescription string
		continueSession bool
		maxBudgetUSD    float64
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "new session",
			taskDescription: "Fix the bug in handler.go",
			continueSession: false,
			wantContains: []string{
				"#!/bin/bash",
				"source ~/.bashrc",
				"set -o pipefail",
				"--dangerously-skip-permissions",
				"--effort max",
				"--output-format stream-json",
				"--append-system-prompt-file ~/system-prompt.md",
				"'Fix the bug in handler.go'",
				"tee  ~/transcript.jsonl",
			},
			wantNotContains: []string{
				"--continue",
				"tee -a",
				"cd ~/project",
				"--max-budget-usd",
			},
		},
		{
			name:            "continue session",
			taskDescription: "Also fix the tests",
			continueSession: true,
			wantContains: []string{
				"--continue",
				"--append-system-prompt-file ~/system-prompt.md",
				"'Also fix the tests'",
				"tee -a ~/transcript.jsonl",
			},
		},
		{
			name:            "description with single quotes",
			taskDescription: "Fix the 'bug' in handler.go",
			continueSession: false,
			wantContains: []string{
				`'Fix the '\''bug'\'' in handler.go'`,
			},
		},
		{
			name:            "with max budget",
			taskDescription: "Fix the bug",
			continueSession: false,
			maxBudgetUSD:    100,
			wantContains: []string{
				"--max-budget-usd 100.00",
				"'Fix the bug'",
			},
		},
		{
			name:            "zero budget omits flag",
			taskDescription: "Fix the bug",
			continueSession: false,
			maxBudgetUSD:    0,
			wantNotContains: []string{
				"--max-budget-usd",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRunScript(tt.taskDescription, tt.continueSession, tt.maxBudgetUSD)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("buildRunScript() missing %q\ngot:\n%s", want, got)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("buildRunScript() should not contain %q\ngot:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestWriteBudgetJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			MaxBudgetUSD:      100,
			WarnBudgetUSD:     30,
			CriticalBudgetUSD: 50,
		},
	}

	if err := writeBudgetJSON(cfg, dir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]float64
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if got["max_budget_usd"] != 100 {
		t.Errorf("max_budget_usd = %v, want 100", got["max_budget_usd"])
	}
	if got["warn_budget_usd"] != 30 {
		t.Errorf("warn_budget_usd = %v, want 30", got["warn_budget_usd"])
	}
	if got["critical_budget_usd"] != 50 {
		t.Errorf("critical_budget_usd = %v, want 50", got["critical_budget_usd"])
	}
}

func TestWriteBudgetJSON_zeroes(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}

	if err := writeBudgetJSON(cfg, dir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "budget.json"))
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]float64
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if got["max_budget_usd"] != 0 {
		t.Errorf("max_budget_usd = %v, want 0", got["max_budget_usd"])
	}
}

func TestResolveTaskSource(t *testing.T) {
	outputDir := t.TempDir()
	td, err := task.Create(outputDir, "my-task")
	if err != nil {
		t.Fatal(err)
	}
	if err := td.SaveMetadata("my-task", "desc", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := resolveTaskSource(td, "org/tasks", 42, ""); !ok {
		t.Error("expected ok from explicit source")
	}
	if repo, num, ok := resolveTaskSource(td, "org/tasks", 42, ""); !ok || repo != "org/tasks" || num != 42 {
		t.Errorf("got %q %d %v", repo, num, ok)
	}

	if err := td.SaveSource("org/tasks", 99); err != nil {
		t.Fatal(err)
	}
	if repo, num, ok := resolveTaskSource(td, "", 0, "fallback/tasks"); !ok || repo != "org/tasks" || num != 99 {
		t.Errorf("from state: got %q %d %v", repo, num, ok)
	}

	td2, err := task.Create(outputDir, "other-task")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := resolveTaskSource(td2, "", 0, ""); ok {
		t.Error("expected not ok without source")
	}
}
