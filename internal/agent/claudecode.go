package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/drellahq/orchestrator/internal/shellutil"
)

type claudeCode struct {
	llmBaseURL string
}

func (b *claudeCode) Name() string { return "claude-code" }

func (b *claudeCode) InstallCmd() string {
	return "curl -fsSL https://claude.ai/install.sh | bash"
}

func (b *claudeCode) BinaryPath() string {
	return `export PATH="$HOME/.local/bin:$PATH"`
}

func (b *claudeCode) BuildRunScript(taskDescription string, continueSession bool, systemPromptFile string, maxBudgetUSD float64, _ time.Duration) string {
	escapedDesc := strings.ReplaceAll(taskDescription, "'", "'\\''")

	var claudeFlags string
	var teeFlag string
	if continueSession {
		claudeFlags = "--continue"
		teeFlag = "-a"
	}

	var systemPromptFlag string
	if systemPromptFile != "" {
		systemPromptFlag = "--append-system-prompt-file " + systemPromptFile + " "
	}

	var budgetFlag string
	if maxBudgetUSD > 0 {
		budgetFlag = fmt.Sprintf("--max-budget-usd %.2f ", maxBudgetUSD)
	}

	var apiKeyLine string
	if b.llmBaseURL != "" {
		apiKeyLine = llmEnvBlock(b.llmBaseURL)
	} else {
		apiKeyLine = `export ANTHROPIC_API_KEY="$(cat ~/.anthropic/api_key 2>/dev/null || true)"` + "\n"
	}

	return fmt.Sprintf(`#!/bin/bash
source ~/.bashrc
export PATH="$HOME/.local/bin:$PATH"
%sset -o pipefail
stdbuf -oL claude --dangerously-skip-permissions -p --verbose \
  --effort max \
  --output-format stream-json %s%s\
  %s '%s' \
  </dev/null | stdbuf -oL tee %s ~/transcript.jsonl
`, apiKeyLine, budgetFlag, systemPromptFlag, claudeFlags, escapedDesc, teeFlag)
}

func (b *claudeCode) MCPAddCmd(name, transport, url, scope string) string {
	cmd := fmt.Sprintf("claude mcp add --transport %s", transport)
	if scope != "" {
		cmd += " --scope " + shellutil.Quote(scope)
	}
	cmd += " " + shellutil.Quote(name) + " " + shellutil.Quote(url)
	return cmd
}

func (b *claudeCode) ConfigDir() string      { return ".claude" }
func (b *claudeCode) ConversationDir() string { return ".claude" }

func (b *claudeCode) FormatTranscriptLine(line []byte, verbose bool) string {
	var msg struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type     string          `json:"type"`
				Text     string          `json:"text"`
				Name     string          `json:"name"`
				Input    json.RawMessage `json:"input"`
				Thinking string          `json:"thinking"`
				Content  json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"message"`
		DurationMS   int     `json:"duration_ms"`
		NumTurns     int     `json:"num_turns"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return ""
	}

	var out string
	switch msg.Type {
	case "assistant":
		for _, c := range msg.Message.Content {
			switch c.Type {
			case "text":
				out += c.Text + "\n"
			case "tool_use":
				summary := toolInputSummary(c.Name, c.Input)
				if summary != "" {
					out += fmt.Sprintf("[tool] %s: %s\n", c.Name, summary)
				} else {
					out += fmt.Sprintf("[tool] %s\n", c.Name)
				}
			case "thinking":
				if verbose && c.Thinking != "" {
					out += fmt.Sprintf("[thinking] %s\n", c.Thinking)
				}
			}
		}
	case "user":
		for _, c := range msg.Message.Content {
			if c.Type != "tool_result" || len(c.Content) == 0 {
				continue
			}
			var s string
			if json.Unmarshal(c.Content, &s) == nil && s != "" {
				out += fmt.Sprintf("  → %s\n", firstLine(s, 200))
			}
		}
	case "result":
		subtype := msg.Subtype
		if subtype == "" {
			subtype = "done"
		}
		duration := float64(msg.DurationMS) / 1000
		tokens := ""
		if msg.Usage != nil && (msg.Usage.InputTokens > 0 || msg.Usage.OutputTokens > 0 || msg.Usage.CacheReadInputTokens > 0 || msg.Usage.CacheCreationInputTokens > 0) {
			var parts []string
			if msg.Usage.CacheReadInputTokens > 0 {
				parts = append(parts, formatTokenCount(msg.Usage.CacheReadInputTokens)+"↺")
			}
			if msg.Usage.CacheCreationInputTokens > 0 {
				parts = append(parts, formatTokenCount(msg.Usage.CacheCreationInputTokens)+"⊕")
			}
			parts = append(parts, formatTokenCount(msg.Usage.InputTokens)+"↑")
			parts = append(parts, formatTokenCount(msg.Usage.OutputTokens)+"↓")
			tokens = ", " + strings.Join(parts, " ")
		}
		if msg.TotalCostUSD > 0 {
			out = fmt.Sprintf("[result] %s (%d turns, %.1fs, $%.4f%s)\n", subtype, msg.NumTurns, duration, msg.TotalCostUSD, tokens)
		} else if msg.DurationMS > 0 {
			out = fmt.Sprintf("[result] %s (%d turns, %.1fs%s)\n", subtype, msg.NumTurns, duration, tokens)
		} else {
			out = fmt.Sprintf("[result] %s\n", subtype)
		}
	}
	return out
}

func (b *claudeCode) ParseResultEntry(line []byte) *UsageEntry {
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
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil
	}
	if entry.Type != "result" {
		return nil
	}
	u := &UsageEntry{CostUSD: entry.TotalCostUSD}
	if entry.Usage != nil {
		u.HasUsage = true
		u.InputTokens = entry.Usage.InputTokens
		u.OutputTokens = entry.Usage.OutputTokens
		u.CacheReadInputTokens = entry.Usage.CacheReadInputTokens
		u.CacheCreationInputTokens = entry.Usage.CacheCreationInputTokens
	}
	return u
}
