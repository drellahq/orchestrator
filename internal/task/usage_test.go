package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTranscriptUsage(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantIn    int
		wantOut   int
		wantCost  float64
		wantErr   bool
	}{
		{
			name: "single result with usage",
			content: `{"type":"system","subtype":"init","model":"claude-opus-4-20250514"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","total_cost_usd":0.0834,"usage":{"input_tokens":1500,"output_tokens":340}}
`,
			wantIn:   1500,
			wantOut:  340,
			wantCost: 0.0834,
		},
		{
			name: "multiple runs summed",
			content: `{"type":"result","subtype":"success","total_cost_usd":0.05,"usage":{"input_tokens":1000,"output_tokens":200}}
{"type":"system","subtype":"init","model":"claude-opus-4-20250514"}
{"type":"result","subtype":"success","total_cost_usd":0.03,"usage":{"input_tokens":800,"output_tokens":150}}
`,
			wantIn:   1800,
			wantOut:  350,
			wantCost: 0.08,
		},
		{
			name: "result without usage field",
			content: `{"type":"result","subtype":"success","total_cost_usd":0.02}
`,
			wantIn:   0,
			wantOut:  0,
			wantCost: 0.02,
		},
		{
			name:     "empty transcript",
			content:  "",
			wantIn:   0,
			wantOut:  0,
			wantCost: 0,
		},
		{
			name: "non-result entries ignored",
			content: `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}
`,
			wantIn:   0,
			wantOut:  0,
			wantCost: 0,
		},
		{
			name: "invalid json lines skipped",
			content: `not valid json
{"type":"result","subtype":"success","total_cost_usd":0.01,"usage":{"input_tokens":500,"output_tokens":100}}
also invalid
`,
			wantIn:   500,
			wantOut:  100,
			wantCost: 0.01,
		},
		{
			name: "result with zero cost",
			content: `{"type":"result","subtype":"success","usage":{"input_tokens":200,"output_tokens":50}}
`,
			wantIn:   200,
			wantOut:  50,
			wantCost: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "transcript.jsonl")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			usage, err := ParseTranscriptUsage(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseTranscriptUsage() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}

			if usage.InputTokens != tt.wantIn {
				t.Errorf("InputTokens = %d, want %d", usage.InputTokens, tt.wantIn)
			}
			if usage.OutputTokens != tt.wantOut {
				t.Errorf("OutputTokens = %d, want %d", usage.OutputTokens, tt.wantOut)
			}
			if diff := usage.CostUSD - tt.wantCost; diff > 0.001 || diff < -0.001 {
				t.Errorf("CostUSD = %f, want %f", usage.CostUSD, tt.wantCost)
			}
		})
	}
}

func TestParseTranscriptUsage_MissingFile(t *testing.T) {
	_, err := ParseTranscriptUsage("/nonexistent/transcript.jsonl")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
