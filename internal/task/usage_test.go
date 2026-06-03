package task

import (
	"os"
	"path/filepath"
	"testing"
)

func intPtr(v int) *int { return &v }

func TestParseTranscriptUsage(t *testing.T) {
	tests := []struct {
		name             string
		content          string
		wantIn           int
		wantOut          int
		wantCacheRead    *int
		wantCacheCreate  *int
		wantCost         float64
		wantErr          bool
	}{
		{
			name: "single result with usage",
			content: `{"type":"system","subtype":"init","model":"claude-opus-4-20250514"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","total_cost_usd":0.0834,"usage":{"input_tokens":1500,"output_tokens":340}}
`,
			wantIn:          1500,
			wantOut:         340,
			wantCacheRead:   intPtr(0),
			wantCacheCreate: intPtr(0),
			wantCost:        0.0834,
		},
		{
			name: "multiple runs summed",
			content: `{"type":"result","subtype":"success","total_cost_usd":0.05,"usage":{"input_tokens":1000,"output_tokens":200}}
{"type":"system","subtype":"init","model":"claude-opus-4-20250514"}
{"type":"result","subtype":"success","total_cost_usd":0.03,"usage":{"input_tokens":800,"output_tokens":150}}
`,
			wantIn:          1800,
			wantOut:         350,
			wantCacheRead:   intPtr(0),
			wantCacheCreate: intPtr(0),
			wantCost:        0.08,
		},
		{
			name: "result without usage field",
			content: `{"type":"result","subtype":"success","total_cost_usd":0.02}
`,
			wantIn:          0,
			wantOut:         0,
			wantCacheRead:   nil,
			wantCacheCreate: nil,
			wantCost:        0.02,
		},
		{
			name:            "empty transcript",
			content:         "",
			wantIn:          0,
			wantOut:         0,
			wantCacheRead:   nil,
			wantCacheCreate: nil,
			wantCost:        0,
		},
		{
			name: "non-result entries ignored",
			content: `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}
`,
			wantIn:          0,
			wantOut:         0,
			wantCacheRead:   nil,
			wantCacheCreate: nil,
			wantCost:        0,
		},
		{
			name: "invalid json lines skipped",
			content: `not valid json
{"type":"result","subtype":"success","total_cost_usd":0.01,"usage":{"input_tokens":500,"output_tokens":100}}
also invalid
`,
			wantIn:          500,
			wantOut:         100,
			wantCacheRead:   intPtr(0),
			wantCacheCreate: intPtr(0),
			wantCost:        0.01,
		},
		{
			name: "result with zero cost",
			content: `{"type":"result","subtype":"success","usage":{"input_tokens":200,"output_tokens":50}}
`,
			wantIn:          200,
			wantOut:         50,
			wantCacheRead:   intPtr(0),
			wantCacheCreate: intPtr(0),
			wantCost:        0,
		},
		{
			name: "result with cache tokens",
			content: `{"type":"result","subtype":"success","total_cost_usd":15.9941,"usage":{"input_tokens":131,"output_tokens":39300,"cache_read_input_tokens":284000,"cache_creation_input_tokens":18000}}
`,
			wantIn:          131,
			wantOut:         39300,
			wantCacheRead:   intPtr(284000),
			wantCacheCreate: intPtr(18000),
			wantCost:        15.9941,
		},
		{
			name: "multiple runs with cache tokens summed",
			content: `{"type":"result","subtype":"success","total_cost_usd":5.0,"usage":{"input_tokens":100,"output_tokens":2000,"cache_read_input_tokens":140000,"cache_creation_input_tokens":10000}}
{"type":"result","subtype":"success","total_cost_usd":3.0,"usage":{"input_tokens":31,"output_tokens":1500,"cache_read_input_tokens":144000,"cache_creation_input_tokens":8000}}
`,
			wantIn:          131,
			wantOut:         3500,
			wantCacheRead:   intPtr(284000),
			wantCacheCreate: intPtr(18000),
			wantCost:        8.0,
		},
		{
			name: "result with zero cache tokens",
			content: `{"type":"result","subtype":"success","total_cost_usd":0.05,"usage":{"input_tokens":500,"output_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}
`,
			wantIn:          500,
			wantOut:         100,
			wantCacheRead:   intPtr(0),
			wantCacheCreate: intPtr(0),
			wantCost:        0.05,
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

			if tt.wantCacheRead == nil {
				if usage.CacheReadInputTokens != nil {
					t.Errorf("CacheReadInputTokens = %d, want nil", *usage.CacheReadInputTokens)
				}
			} else {
				if usage.CacheReadInputTokens == nil {
					t.Errorf("CacheReadInputTokens = nil, want %d", *tt.wantCacheRead)
				} else if *usage.CacheReadInputTokens != *tt.wantCacheRead {
					t.Errorf("CacheReadInputTokens = %d, want %d", *usage.CacheReadInputTokens, *tt.wantCacheRead)
				}
			}

			if tt.wantCacheCreate == nil {
				if usage.CacheCreationInputTokens != nil {
					t.Errorf("CacheCreationInputTokens = %d, want nil", *usage.CacheCreationInputTokens)
				}
			} else {
				if usage.CacheCreationInputTokens == nil {
					t.Errorf("CacheCreationInputTokens = nil, want %d", *tt.wantCacheCreate)
				} else if *usage.CacheCreationInputTokens != *tt.wantCacheCreate {
					t.Errorf("CacheCreationInputTokens = %d, want %d", *usage.CacheCreationInputTokens, *tt.wantCacheCreate)
				}
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
