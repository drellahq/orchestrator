package agent

import (
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"", "claude-code", false},
		{"claude-code", "claude-code", false},
		{"opencode", "opencode", false},
		{"unknown", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := New(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%q) error = %v, wantErr = %v", tt.name, err, tt.wantErr)
			}
			if err == nil && b.Name() != tt.want {
				t.Errorf("New(%q).Name() = %q, want %q", tt.name, b.Name(), tt.want)
			}
		})
	}
}

func TestClaudeCodeBuildRunScript(t *testing.T) {
	b := &claudeCode{}
	script := b.BuildRunScript("fix the bug", false, "~/system-prompt.md")

	if got := script; got == "" {
		t.Fatal("empty script")
	}
	for _, want := range []string{
		"claude --dangerously-skip-permissions",
		"--output-format stream-json",
		"--effort max",
		"--append-system-prompt-file ~/system-prompt.md",
		"fix the bug",
		"tee",
		"transcript.jsonl",
	} {
		if !contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}

	// Continue session
	script = b.BuildRunScript("continue task", true, "")
	if !contains(script, "--continue") {
		t.Error("continue script missing --continue flag")
	}
	if !contains(script, "tee -a") {
		t.Error("continue script missing tee -a flag")
	}
}

func TestOpenCodeBuildRunScript(t *testing.T) {
	b := &openCode{}
	script := b.BuildRunScript("fix the bug", false, "~/system-prompt.md")

	if got := script; got == "" {
		t.Fatal("empty script")
	}
	for _, want := range []string{
		"opencode run --dangerously-skip-permissions",
		"--format json",
		"--variant max",
		"fix the bug",
		"tee",
		"transcript.jsonl",
	} {
		if !contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}

	// Should NOT contain claude-specific flags
	for _, bad := range []string{
		"--output-format",
		"--effort",
		"--append-system-prompt-file",
	} {
		if contains(script, bad) {
			t.Errorf("script should not contain %q", bad)
		}
	}
}

func TestClaudeCodeMCPAddCmd(t *testing.T) {
	b := &claudeCode{}
	cmd := b.MCPAddCmd("orchestrator", "http", "http://localhost:19090/mcp", "user")
	want := "claude mcp add --transport http --scope user orchestrator http://localhost:19090/mcp"
	if cmd != want {
		t.Errorf("MCPAddCmd = %q, want %q", cmd, want)
	}
}

func TestOpenCodeMCPAddCmd(t *testing.T) {
	b := &openCode{}
	cmd := b.MCPAddCmd("orchestrator", "http", "http://localhost:19090/mcp", "user")
	if !contains(cmd, `"orchestrator"`) {
		t.Error("MCP config missing server name")
	}
	if !contains(cmd, `"remote"`) {
		t.Error("MCP config missing type")
	}
	if !contains(cmd, "http://localhost:19090/mcp") {
		t.Error("MCP config missing URL")
	}
	if !contains(cmd, "opencode.json") {
		t.Error("MCP config should write opencode.json")
	}
}

func TestClaudeCodeFormatTranscriptLine(t *testing.T) {
	b := &claudeCode{}

	tests := []struct {
		name    string
		line    string
		verbose bool
		want    string
	}{
		{
			"text message",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}`,
			false,
			"hello world\n",
		},
		{
			"tool use",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls","description":"list files"}}]}}`,
			false,
			"[tool] Bash: list files\n",
		},
		{
			"result",
			`{"type":"result","subtype":"done","duration_ms":5000,"num_turns":3,"total_cost_usd":0.05,"usage":{"input_tokens":1000,"output_tokens":200}}`,
			false,
			"[result] done (3 turns, 5.0s, $0.0500, 1.0k↑ 200↓)\n",
		},
		{
			"thinking hidden",
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"let me think"}]}}`,
			false,
			"",
		},
		{
			"thinking shown",
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"let me think"}]}}`,
			true,
			"[thinking] let me think\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.FormatTranscriptLine([]byte(tt.line), tt.verbose)
			if got != tt.want {
				t.Errorf("FormatTranscriptLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenCodeFormatTranscriptLine(t *testing.T) {
	b := &openCode{}

	tests := []struct {
		name    string
		line    string
		verbose bool
		want    string
	}{
		{
			"text message",
			`{"type":"text","timestamp":123,"sessionID":"s1","part":{"type":"text","text":"hello world"}}`,
			false,
			"hello world\n",
		},
		{
			"tool use bash",
			`{"type":"tool_use","timestamp":123,"sessionID":"s1","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"ls","description":"list files"},"output":"file1\n"}}}`,
			false,
			"[tool] bash: list files\n",
		},
		{
			"step finish with cost",
			`{"type":"step_finish","timestamp":123,"sessionID":"s1","part":{"reason":"stop","tokens":{"total":1200,"input":1000,"output":200},"cost":0.05}}`,
			false,
			"[result] done ($0.0500, 1.0k↑ 200↓)\n",
		},
		{
			"step finish tool-calls (not final)",
			`{"type":"step_finish","timestamp":123,"sessionID":"s1","part":{"reason":"tool-calls","tokens":{"total":100,"input":80,"output":20}}}`,
			false,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.FormatTranscriptLine([]byte(tt.line), tt.verbose)
			if got != tt.want {
				t.Errorf("FormatTranscriptLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaudeCodeParseResultEntry(t *testing.T) {
	b := &claudeCode{}

	result := b.ParseResultEntry([]byte(`{"type":"result","total_cost_usd":0.05,"usage":{"input_tokens":1000,"output_tokens":200}}`))
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", result.CostUSD)
	}
	if result.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", result.InputTokens)
	}

	// Non-result lines return nil
	if b.ParseResultEntry([]byte(`{"type":"assistant"}`)) != nil {
		t.Error("non-result line should return nil")
	}
}

func TestOpenCodeParseResultEntry(t *testing.T) {
	b := &openCode{}

	result := b.ParseResultEntry([]byte(`{"type":"step_finish","part":{"reason":"stop","tokens":{"total":1200,"input":1000,"output":200},"cost":0.05}}`))
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", result.CostUSD)
	}
	if result.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", result.InputTokens)
	}

	// tool-calls step_finish should not count as result
	if b.ParseResultEntry([]byte(`{"type":"step_finish","part":{"reason":"tool-calls"}}`)) != nil {
		t.Error("tool-calls step_finish should return nil")
	}
}

func TestConfigDir(t *testing.T) {
	cc := &claudeCode{}
	oc := &openCode{}

	if cc.ConfigDir() != ".claude" {
		t.Errorf("claude ConfigDir = %q", cc.ConfigDir())
	}
	if oc.ConfigDir() != ".opencode" {
		t.Errorf("opencode ConfigDir = %q", oc.ConfigDir())
	}
}

func TestInstallCmd(t *testing.T) {
	cc := &claudeCode{}
	oc := &openCode{}

	if !contains(cc.InstallCmd(), "claude.ai") {
		t.Error("claude InstallCmd should reference claude.ai")
	}
	if !contains(oc.InstallCmd(), "opencode.ai") {
		t.Error("opencode InstallCmd should reference opencode.ai")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
