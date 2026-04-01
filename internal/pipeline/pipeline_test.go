package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellabot/orchestrator/internal/config"
)

func TestNewState(t *testing.T) {
	steps := []config.PipelineStep{
		{Role: "producer", MaxIterations: 1},
		{Role: "validator", MaxIterations: 3},
	}
	state := NewState("default", steps)

	if state.Pipeline != "default" {
		t.Errorf("Pipeline = %q, want %q", state.Pipeline, "default")
	}
	if state.CurrentStep != 0 {
		t.Errorf("CurrentStep = %d, want 0", state.CurrentStep)
	}
	if state.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", state.Iteration)
	}
	if len(state.Steps) != 2 {
		t.Fatalf("Steps = %d, want 2", len(state.Steps))
	}
	if state.Steps[0].Role != "producer" {
		t.Errorf("Steps[0].Role = %q, want %q", state.Steps[0].Role, "producer")
	}
	if state.Steps[0].Status != "pending" {
		t.Errorf("Steps[0].Status = %q, want %q", state.Steps[0].Status, "pending")
	}
	if state.Steps[1].Role != "validator" {
		t.Errorf("Steps[1].Role = %q, want %q", state.Steps[1].Role, "validator")
	}
}

func TestIsMultiStep(t *testing.T) {
	tests := []struct {
		name  string
		steps []config.PipelineStep
		want  bool
	}{
		{
			name:  "single step",
			steps: []config.PipelineStep{{Role: "producer"}},
			want:  false,
		},
		{
			name:  "multi step",
			steps: []config.PipelineStep{{Role: "producer"}, {Role: "validator"}},
			want:  true,
		},
		{
			name:  "empty",
			steps: nil,
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMultiStep(tt.steps); got != tt.want {
				t.Errorf("IsMultiStep() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTranscriptName(t *testing.T) {
	tests := []struct {
		role      string
		iteration int
		multiStep bool
		want      string
	}{
		{"producer", 1, false, "transcript.jsonl"},
		{"producer", 1, true, "transcript-producer-1.jsonl"},
		{"validator", 2, true, "transcript-validator-2.jsonl"},
		{"producer", 3, true, "transcript-producer-3.jsonl"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := TranscriptName(tt.role, tt.iteration, tt.multiStep); got != tt.want {
				t.Errorf("TranscriptName(%q, %d, %v) = %q, want %q", tt.role, tt.iteration, tt.multiStep, got, tt.want)
			}
		})
	}
}

func TestBuildAgentSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tester.md"), []byte("## Test Role\nRun tests."), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := BuildAgentSystemPrompt(dir, "tester")
	if err != nil {
		t.Fatalf("BuildAgentSystemPrompt() error: %v", err)
	}

	// Should contain both the base prompt and the role prompt.
	if !strings.Contains(got, "sandboxed VM") {
		t.Error("missing base prompt content")
	}
	if !strings.Contains(got, "## Test Role") {
		t.Error("missing role prompt content")
	}
}

func TestBuildAgentSystemPrompt_MissingRole(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildAgentSystemPrompt(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing role file")
	}
}

func TestBuildHandoffPrompt(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name     string
		workItem string
		diff     string
		handoff  config.HandoffConfig
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:     "default handoff includes diff",
			workItem: "Fix the bug",
			diff:     "+added line\n-removed line",
			handoff:  config.HandoffConfig{},
			wantContains: []string{
				"Fix the bug",
				"Changes from prior agent",
				"+added line",
			},
		},
		{
			name:     "diff disabled",
			workItem: "Fix the bug",
			diff:     "+added line",
			handoff:  config.HandoffConfig{IncludeDiff: boolPtr(false)},
			wantContains:    []string{"Fix the bug"},
			wantNotContains: []string{"Changes from prior agent"},
		},
		{
			name:     "empty diff not included",
			workItem: "Fix the bug",
			diff:     "",
			handoff:  config.HandoffConfig{},
			wantContains:    []string{"Fix the bug"},
			wantNotContains: []string{"Changes from prior agent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildHandoffPrompt(tt.workItem, tt.diff, "", tt.handoff)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("should not contain %q in:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestBuildFeedbackPrompt(t *testing.T) {
	got := BuildFeedbackPrompt("Implement feature X", "- Missing test for edge case\n- Error handling incomplete")
	if !strings.Contains(got, "Implement feature X") {
		t.Error("missing work item")
	}
	if !strings.Contains(got, "Feedback from reviewer") {
		t.Error("missing feedback header")
	}
	if !strings.Contains(got, "Missing test for edge case") {
		t.Error("missing findings")
	}
}

func TestParseVerdict(t *testing.T) {
	makeTranscript := func(lines ...string) []byte {
		var result []byte
		for _, text := range lines {
			msg := map[string]any{
				"type": "assistant",
				"message": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": text},
					},
				},
			}
			data, _ := json.Marshal(msg)
			result = append(result, data...)
			result = append(result, '\n')
		}
		return result
	}

	tests := []struct {
		name        string
		transcript  []byte
		wantVerdict Verdict
		wantErr     bool
	}{
		{
			name:        "pass verdict",
			transcript:  makeTranscript("Everything looks good.\n\nVERDICT: pass"),
			wantVerdict: VerdictPass,
		},
		{
			name:        "fail verdict",
			transcript:  makeTranscript("Issues found:\n- Missing tests\n\nVERDICT: fail"),
			wantVerdict: VerdictFail,
		},
		{
			name:        "verdict in last message only",
			transcript:  makeTranscript("Reviewing code...", "Found issues:\n- Bug in handler\n\nVERDICT: fail"),
			wantVerdict: VerdictFail,
		},
		{
			name:       "no verdict line",
			transcript: makeTranscript("I reviewed the code and it looks fine."),
			wantErr:    true,
		},
		{
			name:       "empty transcript",
			transcript: []byte{},
			wantErr:    true,
		},
		{
			name:       "unknown verdict value",
			transcript: makeTranscript("VERDICT: maybe"),
			wantErr:    true,
		},
		{
			name:        "verdict with extra whitespace",
			transcript:  makeTranscript("VERDICT:   pass  "),
			wantVerdict: VerdictPass,
		},
		{
			name: "non-assistant messages ignored",
			transcript: func() []byte {
				var result []byte
				userMsg, _ := json.Marshal(map[string]any{
					"type": "user",
					"message": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "VERDICT: pass"},
						},
					},
				})
				result = append(result, userMsg...)
				result = append(result, '\n')

				assistantMsg, _ := json.Marshal(map[string]any{
					"type": "assistant",
					"message": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "VERDICT: fail"},
						},
					},
				})
				result = append(result, assistantMsg...)
				result = append(result, '\n')
				return result
			}(),
			wantVerdict: VerdictFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, _, err := ParseVerdict(tt.transcript)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if verdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q", verdict, tt.wantVerdict)
			}
		})
	}
}

func TestParseVerdict_ReturnsFindings(t *testing.T) {
	msg := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Found issues:\n- Missing test for X\n- Bug in Y\n\nVERDICT: fail"},
			},
		},
	}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')

	_, findings, err := ParseVerdict(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(findings, "Missing test for X") {
		t.Error("findings should contain the validator's text")
	}
}

func TestEscalationComment(t *testing.T) {
	findings := []string{
		"- Missing test coverage\n- Error handling incomplete",
		"- Still missing test coverage",
	}
	got := EscalationComment(2, findings)

	if !strings.Contains(got, "max iterations reached") {
		t.Error("missing escalation header")
	}
	if !strings.Contains(got, "Iteration 1") {
		t.Error("missing iteration 1 header")
	}
	if !strings.Contains(got, "Iteration 2") {
		t.Error("missing iteration 2 header")
	}
	if !strings.Contains(got, "Missing test coverage") {
		t.Error("missing findings content")
	}
}

func TestEscalationComment_TruncatesLongFindings(t *testing.T) {
	longFinding := strings.Repeat("x", 5000)
	got := EscalationComment(1, []string{longFinding})
	if !strings.Contains(got, "truncated") {
		t.Error("expected truncation marker for long findings")
	}
	if len(got) > 6000 {
		t.Errorf("comment too long: %d chars", len(got))
	}
}

func TestExtractAssistantText(t *testing.T) {
	tests := []struct {
		name string
		line []byte
		want string
	}{
		{
			name: "assistant text",
			line: func() []byte {
				d, _ := json.Marshal(map[string]any{
					"type":    "assistant",
					"message": map[string]any{"content": []map[string]any{{"type": "text", "text": "hello"}}},
				})
				return d
			}(),
			want: "hello",
		},
		{
			name: "user message ignored",
			line: func() []byte {
				d, _ := json.Marshal(map[string]any{
					"type":    "user",
					"message": map[string]any{"content": []map[string]any{{"type": "text", "text": "hello"}}},
				})
				return d
			}(),
			want: "",
		},
		{
			name: "tool_use content ignored",
			line: func() []byte {
				d, _ := json.Marshal(map[string]any{
					"type": "assistant",
					"message": map[string]any{"content": []map[string]any{
						{"type": "tool_use", "text": ""},
						{"type": "text", "text": "actual text"},
					}},
				})
				return d
			}(),
			want: "actual text",
		},
		{
			name: "invalid json",
			line: []byte("not json"),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAssistantText(tt.line); got != tt.want {
				t.Errorf("extractAssistantText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStateJSON(t *testing.T) {
	steps := []config.PipelineStep{
		{Role: "producer", MaxIterations: 1},
		{Role: "validator", MaxIterations: 3},
	}
	state := NewState("default", steps)
	state.Steps[0].Status = "completed"
	state.Steps[0].Iterations = 1
	state.Steps[1].Status = "running"
	state.Steps[1].Verdict = VerdictFail

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	var decoded State
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.Pipeline != "default" {
		t.Errorf("Pipeline = %q, want %q", decoded.Pipeline, "default")
	}
	if decoded.Steps[1].Verdict != VerdictFail {
		t.Errorf("Steps[1].Verdict = %q, want %q", decoded.Steps[1].Verdict, VerdictFail)
	}
}

func TestHandoffConfigDefaults(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	// Zero-value HandoffConfig should use defaults.
	h := config.HandoffConfig{}
	if !h.IncludeDiffOrDefault() {
		t.Error("IncludeDiffOrDefault() should be true by default")
	}
	if h.IncludePriorTranscriptOrDefault() {
		t.Error("IncludePriorTranscriptOrDefault() should be false by default")
	}
	if h.IncludePriorSummaryOrDefault() {
		t.Error("IncludePriorSummaryOrDefault() should be false by default")
	}

	// Explicit false overrides.
	h2 := config.HandoffConfig{IncludeDiff: boolPtr(false)}
	if h2.IncludeDiffOrDefault() {
		t.Error("IncludeDiffOrDefault() should be false when explicitly set")
	}

	// Explicit true overrides.
	h3 := config.HandoffConfig{IncludePriorTranscript: boolPtr(true)}
	if !h3.IncludePriorTranscriptOrDefault() {
		t.Error("IncludePriorTranscriptOrDefault() should be true when explicitly set")
	}
}

func TestBuildHandoffPromptWithTranscript(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	handoff := config.HandoffConfig{
		IncludeDiff:            boolPtr(true),
		IncludePriorTranscript: boolPtr(true),
	}
	got := BuildHandoffPrompt("Fix bug", "+line", "I made changes to X", handoff)

	if !strings.Contains(got, "Fix bug") {
		t.Error("missing work item")
	}
	if !strings.Contains(got, "+line") {
		t.Error("missing diff")
	}
	if !strings.Contains(got, "Prior agent's final message") {
		t.Error("missing transcript section")
	}
	if !strings.Contains(got, "I made changes to X") {
		t.Error("missing transcript content")
	}
}

func TestConfigPipelineDefault(t *testing.T) {
	// No pipelines configured — should return single producer step.
	cfg := &config.Config{}
	steps := cfg.Pipeline("")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Role != "producer" {
		t.Errorf("Role = %q, want %q", steps[0].Role, "producer")
	}
}

func TestConfigPipelineNamed(t *testing.T) {
	cfg := &config.Config{
		Pipelines: map[string][]config.PipelineStep{
			"default": {
				{Role: "producer"},
				{Role: "validator", MaxIterations: 3},
			},
		},
	}
	steps := cfg.Pipeline("")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[1].Role != "validator" {
		t.Errorf("Step[1].Role = %q, want %q", steps[1].Role, "validator")
	}

	// Non-existent pipeline name falls back to single producer.
	steps2 := cfg.Pipeline("nonexistent")
	if len(steps2) != 1 {
		t.Fatalf("expected 1 step for unknown pipeline, got %d", len(steps2))
	}
}

func TestConfigLoadWithPipeline(t *testing.T) {
	dir := t.TempDir()
	yaml := `
pipelines:
  default:
    - role: producer
    - role: validator
      max_iterations: 5
      handoff:
        include_diff: true
        include_prior_transcript: false
agents_dir: "./custom-agents"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.AgentsDir != "./custom-agents" {
		t.Errorf("AgentsDir = %q, want %q", cfg.AgentsDir, "./custom-agents")
	}

	steps := cfg.Pipeline("")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[1].MaxIterations != 5 {
		t.Errorf("MaxIterations = %d, want 5", steps[1].MaxIterations)
	}
	if !steps[1].Handoff.IncludeDiffOrDefault() {
		t.Error("IncludeDiff should be true")
	}
}

func TestConfigLoadDefaultMaxIterations(t *testing.T) {
	dir := t.TempDir()
	yaml := `
pipelines:
  default:
    - role: producer
    - role: validator
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	steps := cfg.Pipeline("")
	if steps[1].MaxIterations != 3 {
		t.Errorf("MaxIterations = %d, want 3 (default)", steps[1].MaxIterations)
	}
}

func TestConfigLoadDefaultAgentsDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.AgentsDir != "./agents" {
		t.Errorf("AgentsDir = %q, want %q", cfg.AgentsDir, "./agents")
	}
}

func TestParseVerdict_MultipleTextBlocks(t *testing.T) {
	// Test that multiple text blocks in a single message are joined.
	msg := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Part 1: reviewing code"},
				{"type": "text", "text": "Part 2: issues found\n\nVERDICT: fail"},
			},
		},
	}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')

	verdict, _, err := ParseVerdict(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict != VerdictFail {
		t.Errorf("verdict = %q, want %q", verdict, VerdictFail)
	}
}

func TestLoadAgentPrompt(t *testing.T) {
	t.Run("loads existing prompt", func(t *testing.T) {
		dir := t.TempDir()
		content := "## Role\nDo stuff.\n"
		if err := os.WriteFile(filepath.Join(dir, "myrole.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := loadAgentPrompt(dir, "myrole")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != content {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("errors on missing prompt", func(t *testing.T) {
		dir := t.TempDir()
		_, err := loadAgentPrompt(dir, "missing")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

// loadAgentPrompt is a helper to test prompts.LoadAgentPrompt from this package.
func loadAgentPrompt(dir, role string) (string, error) {
	path := filepath.Join(dir, role+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("loading agent prompt %q: %w", role, err)
	}
	return string(data), nil
}
