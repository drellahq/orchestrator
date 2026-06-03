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
	var cacheRead, cacheCreation int
	var hasUsage bool

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var entry struct {
			Type         string  `json:"type"`
			TotalCostUSD float64 `json:"total_cost_usd"`
			Usage        *struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
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
			hasUsage = true
			total.InputTokens += entry.Usage.InputTokens
			total.OutputTokens += entry.Usage.OutputTokens
			cacheRead += entry.Usage.CacheReadInputTokens
			cacheCreation += entry.Usage.CacheCreationInputTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if hasUsage {
		total.CacheReadInputTokens = &cacheRead
		total.CacheCreationInputTokens = &cacheCreation
	}
	return &total, nil
}
