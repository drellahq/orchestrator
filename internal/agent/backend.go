package agent

import (
	"encoding/json"
	"fmt"
	"time"
)

// Backend defines how to install, configure, and invoke a coding agent.
type Backend interface {
	// Name returns the backend identifier ("claude-code" or "opencode").
	Name() string

	// InstallCmd returns the shell command to install the agent in a sandbox.
	InstallCmd() string

	// BinaryPath returns the PATH export needed to find the agent binary.
	BinaryPath() string

	// BuildRunScript builds the shell script that invokes the agent headlessly.
	// opencodeBashTimeout is used by OpenCode only (OPENCODE_EXPERIMENTAL_BASH_DEFAULT_TIMEOUT_MS).
	BuildRunScript(taskDescription string, continueSession bool, systemPromptFile string, maxBudgetUSD float64, opencodeBashTimeout time.Duration) string

	// MCPAddCmd returns a shell command to register an MCP server.
	// For agents that use config files instead of CLI commands, this writes
	// the appropriate config file.
	MCPAddCmd(name, transport, url, scope string) string

	// ConfigDir returns the agent's config directory name (e.g. ".claude").
	ConfigDir() string

	// ConversationDir returns the directory to archive after a run.
	ConversationDir() string

	// FormatTranscriptLine formats a single JSONL line for human readability.
	FormatTranscriptLine(line []byte, verbose bool) string

	// ParseResultEntry extracts usage data from a JSONL line if it's a result
	// entry, returning nil if this line is not a result.
	ParseResultEntry(line []byte) *UsageEntry
}

// UsageEntry holds extracted usage data from a single result event.
type UsageEntry struct {
	CostUSD                  float64
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
	HasUsage                 bool
}

// New returns a Backend for the given name.
// Valid names: "claude-code" (default), "opencode".
func New(name string, opts *Options) (Backend, error) {
	var o Options
	if opts != nil {
		o = *opts
	}
	switch name {
	case "", "claude-code":
		return &claudeCode{llmBaseURL: o.LLMBaseURL}, nil
	case "opencode":
		return &openCode{llmBaseURL: o.LLMBaseURL, llmModel: o.LLMModel}, nil
	default:
		return nil, fmt.Errorf("unknown agent backend %q (valid: claude-code, opencode)", name)
	}
}

// toolInputSummary extracts a short description from a tool's input JSON.
// Shared by both backends.
func toolInputSummary(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}

	switch name {
	case "Write", "Read", "Edit", "write", "read", "edit":
		if v, ok := m["file_path"].(string); ok {
			return v
		}
		if v, ok := m["filePath"].(string); ok {
			return v
		}
	case "Bash", "bash":
		if v, ok := m["description"].(string); ok {
			return v
		}
		if v, ok := m["command"].(string); ok {
			return firstLine(v, 80)
		}
	case "Grep", "Glob", "grep", "glob":
		if v, ok := m["pattern"].(string); ok {
			return v
		}
	}

	for _, key := range []string{"path", "query", "url", "name"} {
		if v, ok := m[key].(string); ok {
			return firstLine(v, 80)
		}
	}
	return ""
}

func firstLine(s string, max int) string {
	for i, c := range s {
		if c == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func formatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
