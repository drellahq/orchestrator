package task

import (
	"bufio"
	"os"

	"github.com/drellabot/orchestrator/internal/agent"
)

// ParseTranscriptUsage reads a streaming JSON transcript and returns the
// aggregated token usage across all result entries (one per run).
// The backend determines how to identify and parse result entries from the
// agent-specific transcript format.
func ParseTranscriptUsage(path string, backend agent.Backend) (*Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var total Usage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		entry := backend.ParseResultEntry(scanner.Bytes())
		if entry == nil {
			continue
		}
		total.CostUSD += entry.CostUSD
		total.InputTokens += entry.InputTokens
		total.OutputTokens += entry.OutputTokens
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &total, nil
}
