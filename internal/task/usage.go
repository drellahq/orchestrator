package task

import (
	"bufio"
	"encoding/json"
	"os"
)

// ParseTranscriptUsage reads a stream-json transcript and returns the
// aggregated token usage across all result entries (one per run).
func ParseTranscriptUsage(path string) (*Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var total Usage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var entry struct {
			Type         string  `json:"type"`
			TotalCostUSD float64 `json:"total_cost_usd"`
			Usage        *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != "result" {
			continue
		}
		total.CostUSD += entry.TotalCostUSD
		if entry.Usage != nil {
			total.InputTokens += entry.Usage.InputTokens
			total.OutputTokens += entry.Usage.OutputTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &total, nil
}
