package cmd

import (
	"bytes"
	"testing"
)

func TestFormatTranscriptLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		verbose bool
		want    string
	}{
		{
			name: "text content",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`,
			want: "Hello world\n",
		},
		{
			name: "tool use without input",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
			want: "[tool] Read\n",
		},
		{
			name: "tool use with file_path input",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/home/fedora/hello.txt","content":"hi"}}]}}`,
			want: "[tool] Write: /home/fedora/hello.txt\n",
		},
		{
			name: "tool use Bash with description",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git add -A && git commit -m 'test'","description":"Stage and commit"}}]}}`,
			want: "[tool] Bash: Stage and commit\n",
		},
		{
			name: "tool use Bash falls back to command",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}`,
			want: "[tool] Bash: ls -la\n",
		},
		{
			name: "tool use Grep with pattern",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"func main"}}]}}`,
			want: "[tool] Grep: func main\n",
		},
		{
			name: "tool use MCP with path fallback",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__orchestrator__open_pr","input":{"path":"~/project"}}]}}`,
			want: "[tool] mcp__orchestrator__open_pr: ~/project\n",
		},
		{
			name: "mixed content",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"Reading file"},{"type":"tool_use","name":"Edit","input":{"file_path":"main.go"}}]}}`,
			want: "Reading file\n[tool] Edit: main.go\n",
		},
		{
			name: "tool result string content",
			line: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"123","content":"File created successfully"}]}}`,
			want: "  → File created successfully\n",
		},
		{
			name: "tool result array content ignored",
			line: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"123","content":[{"type":"tool_reference","tool_name":"Write"}]}]}}`,
			want: "",
		},
		{
			name: "tool result multiline shows first line",
			line: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"123","content":"[master abc1234] Add file\n 1 file changed"}]}}`,
			want: "  → [master abc1234] Add file\n",
		},
		{
			name: "result with stats and tokens",
			line: `{"type":"result","subtype":"success","duration_ms":12092,"num_turns":5,"total_cost_usd":0.08,"usage":{"input_tokens":1500,"output_tokens":340}}`,
			want: "[result] success (5 turns, 12.1s, $0.0800, 1.5k↑ 340↓)\n",
		},
		{
			name: "result with cost but no usage",
			line: `{"type":"result","subtype":"success","duration_ms":12092,"num_turns":5,"total_cost_usd":0.08}`,
			want: "[result] success (5 turns, 12.1s, $0.0800)\n",
		},
		{
			name: "result with duration but no cost",
			line: `{"type":"result","subtype":"success","duration_ms":5000,"num_turns":3}`,
			want: "[result] success (3 turns, 5.0s)\n",
		},
		{
			name: "result with duration and tokens but no cost",
			line: `{"type":"result","subtype":"success","duration_ms":5000,"num_turns":3,"usage":{"input_tokens":200000,"output_tokens":8500}}`,
			want: "[result] success (3 turns, 5.0s, 200.0k↑ 8.5k↓)\n",
		},
		{
			name: "result without stats",
			line: `{"type":"result","subtype":"success"}`,
			want: "[result] success\n",
		},
		{
			name: "result without subtype",
			line: `{"type":"result"}`,
			want: "[result] done\n",
		},
		{
			name: "thinking hidden by default",
			line: `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think about this."}]}}`,
			want: "",
		},
		{
			name:    "thinking shown when verbose",
			line:    `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think about this."}]}}`,
			verbose: true,
			want:    "[thinking] Let me think about this.\n",
		},
		{
			name:    "mixed with thinking verbose",
			line:    `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Planning..."},{"type":"text","text":"Here is my answer"}]}}`,
			verbose: true,
			want:    "[thinking] Planning...\nHere is my answer\n",
		},
		{
			name: "unknown type ignored",
			line: `{"type":"system","message":"hello"}`,
			want: "",
		},
		{
			name: "invalid json ignored",
			line: `not json at all`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTranscriptLine([]byte(tt.line), tt.verbose)
			if got != tt.want {
				t.Errorf("formatTranscriptLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranscriptWriter(t *testing.T) {
	tests := []struct {
		name    string
		verbose bool
		writes  []string
		want    string
	}{
		{
			name: "single complete line",
			writes: []string{
				`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n",
			},
			want: "hi\n",
		},
		{
			name: "line split across writes",
			writes: []string{
				`{"type":"assis`,
				`tant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n",
			},
			want: "hi\n",
		},
		{
			name: "multiple lines in one write",
			writes: []string{
				`{"type":"result","subtype":"success"}` + "\n" +
					`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}` + "\n",
			},
			want: "[result] success\ndone\n",
		},
		{
			name: "skips unknown types",
			writes: []string{
				`{"type":"system"}` + "\n" +
					`{"type":"result"}` + "\n",
			},
			want: "[result] done\n",
		},
		{
			name:    "verbose passes through to formatter",
			verbose: true,
			writes: []string{
				`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}` + "\n",
			},
			want: "[thinking] hmm\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tw := newTranscriptWriter(&buf, tt.verbose)
			for _, w := range tt.writes {
				if _, err := tw.Write([]byte(w)); err != nil {
					t.Fatalf("Write() error: %v", err)
				}
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolInputSummary(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{
			name:  "Write file_path",
			tool:  "Write",
			input: `{"file_path":"/home/fedora/hello.txt","content":"hi"}`,
			want:  "/home/fedora/hello.txt",
		},
		{
			name:  "Read file_path",
			tool:  "Read",
			input: `{"file_path":"/etc/hosts"}`,
			want:  "/etc/hosts",
		},
		{
			name:  "Bash description preferred",
			tool:  "Bash",
			input: `{"command":"git status","description":"Show git status"}`,
			want:  "Show git status",
		},
		{
			name:  "Bash command fallback",
			tool:  "Bash",
			input: `{"command":"echo hello"}`,
			want:  "echo hello",
		},
		{
			name:  "Grep pattern",
			tool:  "Grep",
			input: `{"pattern":"TODO","path":"/home/fedora"}`,
			want:  "TODO",
		},
		{
			name:  "Glob pattern",
			tool:  "Glob",
			input: `{"pattern":"**/*.go"}`,
			want:  "**/*.go",
		},
		{
			name:  "generic path fallback",
			tool:  "mcp__orchestrator__open_pr",
			input: `{"path":"~/project"}`,
			want:  "~/project",
		},
		{
			name:  "generic query fallback",
			tool:  "ToolSearch",
			input: `{"query":"select:Read,Write","max_results":3}`,
			want:  "select:Read,Write",
		},
		{
			name:  "empty input",
			tool:  "Read",
			input: `{}`,
			want:  "",
		},
		{
			name:  "null input",
			tool:  "Read",
			input: ``,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolInputSummary(tt.tool, []byte(tt.input))
			if got != tt.want {
				t.Errorf("toolInputSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{12345, "12.3k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatTokenCount(tt.n)
			if got != tt.want {
				t.Errorf("formatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{name: "short single line", s: "hello", max: 80, want: "hello"},
		{name: "multiline", s: "first\nsecond\nthird", max: 80, want: "first"},
		{name: "truncated", s: "a very long string", max: 10, want: "a very lon…"},
		{name: "multiline and truncated", s: "abcdefghij\nsecond", max: 5, want: "abcde…"},
		{name: "empty", s: "", max: 80, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstLine(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("firstLine() = %q, want %q", got, tt.want)
			}
		})
	}
}
