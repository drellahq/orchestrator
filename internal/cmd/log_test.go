package cmd

import (
	"bytes"
	"testing"

	"github.com/drellahq/orchestrator/internal/agent"
)

func TestTranscriptWriter(t *testing.T) {
	ccBackend, _ := agent.New("claude-code")

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
			tw := newTranscriptWriter(&buf, tt.verbose, ccBackend)
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

func TestTranscriptWriter_OpenCode(t *testing.T) {
	ocBackend, _ := agent.New("opencode")

	writes := []string{
		`{"type":"text","timestamp":123,"sessionID":"s1","part":{"type":"text","text":"hello"}}` + "\n",
		`{"type":"step_finish","timestamp":124,"sessionID":"s1","part":{"reason":"stop","tokens":{"total":1200,"input":1000,"output":200},"cost":0.05}}` + "\n",
	}

	var buf bytes.Buffer
	tw := newTranscriptWriter(&buf, false, ocBackend)
	for _, w := range writes {
		if _, err := tw.Write([]byte(w)); err != nil {
			t.Fatalf("Write() error: %v", err)
		}
	}

	want := "hello\n[result] done ($0.0500, 1.0k↑ 200↓)\n"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
