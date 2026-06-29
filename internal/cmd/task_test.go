package cmd

import (
	"context"
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

func TestHasLabel(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		target string
		want   bool
	}{
		{"exact match", []string{"rhel", "bug"}, "rhel", true},
		{"case insensitive", []string{"RHEL", "bug"}, "rhel", true},
		{"mixed case", []string{"Rhel"}, "rhel", true},
		{"not present", []string{"centos", "bug"}, "rhel", false},
		{"empty labels", []string{}, "rhel", false},
		{"nil labels", nil, "rhel", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasLabel(tt.labels, tt.target); got != tt.want {
				t.Errorf("hasLabel(%v, %q) = %v, want %v", tt.labels, tt.target, got, tt.want)
			}
		})
	}
}

func TestSetupRHELSubscription_MissingEnvVars(t *testing.T) {
	t.Setenv("LIGHTSPEED_CLIENT_ID", "")
	t.Setenv("LIGHTSPEED_CLIENT_SECRET", "")
	t.Setenv("LIGHTSPEED_ORG_ID", "")

	dir := t.TempDir()
	td, err := task.Create(dir, "rhel-test")
	if err != nil {
		t.Fatal(err)
	}

	err = setupRHELSubscription(context.Background(), "rhel-test", td)
	if err == nil {
		t.Fatal("expected error when LIGHTSPEED env vars are missing")
	}
	if got := err.Error(); !strings.Contains(got, "LIGHTSPEED_CLIENT_ID") {
		t.Errorf("error should mention LIGHTSPEED_CLIENT_ID, got: %s", got)
	}
}

func TestSetupRHELSubscription_PartialEnvVars(t *testing.T) {
	t.Setenv("LIGHTSPEED_CLIENT_ID", "some-id")
	t.Setenv("LIGHTSPEED_CLIENT_SECRET", "")
	t.Setenv("LIGHTSPEED_ORG_ID", "some-org")

	dir := t.TempDir()
	td, err := task.Create(dir, "rhel-test-partial")
	if err != nil {
		t.Fatal(err)
	}

	err = setupRHELSubscription(context.Background(), "rhel-test-partial", td)
	if err == nil {
		t.Fatal("expected error when LIGHTSPEED_CLIENT_SECRET is missing")
	}
}
