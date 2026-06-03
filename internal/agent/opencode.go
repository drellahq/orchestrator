package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

type openCode struct{}

func (b *openCode) Name() string { return "opencode" }

func (b *openCode) InstallCmd() string {
	return "curl -fsSL https://opencode.ai/install | bash"
}

func (b *openCode) BinaryPath() string {
	return `export PATH="$HOME/.opencode/bin:$PATH"`
}

func (b *openCode) BuildRunScript(taskDescription string, continueSession bool, systemPromptFile string, maxBudgetUSD float64) string {
	escapedDesc := strings.ReplaceAll(taskDescription, "'", "'\\''")

	var flags string
	var teeFlag string
	if continueSession {
		flags = "--continue"
		teeFlag = "-a"
	}

	// OpenCode lacks --append-system-prompt-file; we handle this by merging
	// the system prompt into CLAUDE.md (which OpenCode reads natively) during
	// sandbox setup. The systemPromptFile parameter is ignored here.

	return fmt.Sprintf(`#!/bin/bash
source ~/.bashrc
export PATH="$HOME/.opencode/bin:$PATH"
stdbuf -oL opencode run --dangerously-skip-permissions \
  --variant max \
  --format json \
  %s '%s' \
  </dev/null | stdbuf -oL tee %s ~/transcript.jsonl
`, flags, escapedDesc, teeFlag)
}

func (b *openCode) MCPAddCmd(name, transport, url, scope string) string {
	// OpenCode uses config-based MCP registration.
	// Write an opencode.json in the project root with the MCP server config.
	mcpType := "remote"
	if transport == "stdio" {
		mcpType = "local"
	}
	config := fmt.Sprintf(`{"mcp":{%q:{"type":%q,"url":%q}}}`, name, mcpType, url)
	return fmt.Sprintf(`mkdir -p ~/.config/opencode && cat > ~/.config/opencode/opencode.json << 'MCPEOF'
%s
MCPEOF`, config)
}

func (b *openCode) ConfigDir() string        { return ".opencode" }
func (b *openCode) ConversationDir() string   { return ".opencode" }

func (b *openCode) FormatTranscriptLine(line []byte, verbose bool) string {
	var msg struct {
		Type      string `json:"type"`
		Timestamp int64  `json:"timestamp"`
		SessionID string `json:"sessionID"`
		Part      struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Tool      string `json:"tool"`
			Reason    string `json:"reason"`
			Thinking  string `json:"thinking"`
			MessageID string `json:"messageID"`
			State     *struct {
				Status string `json:"status"`
				Input  struct {
					Command     string `json:"command"`
					Description string `json:"description"`
					FilePath    string `json:"filePath"`
					Pattern     string `json:"pattern"`
				} `json:"input"`
				Output   string `json:"output"`
				Metadata struct {
					Exit int `json:"exit"`
				} `json:"metadata"`
			} `json:"state"`
			Tokens *struct {
				Total     int `json:"total"`
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
			} `json:"tokens"`
			Cost float64 `json:"cost"`
		} `json:"part"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return ""
	}

	switch msg.Type {
	case "text":
		if msg.Part.Text != "" {
			return msg.Part.Text + "\n"
		}
	case "thinking":
		if verbose && msg.Part.Thinking != "" {
			return fmt.Sprintf("[thinking] %s\n", msg.Part.Thinking)
		}
	case "tool_use":
		tool := msg.Part.Tool
		if tool == "" {
			tool = msg.Part.Type
		}
		var summary string
		if msg.Part.State != nil {
			input := msg.Part.State.Input
			switch tool {
			case "bash":
				if input.Description != "" {
					summary = input.Description
				} else if input.Command != "" {
					summary = firstLine(input.Command, 80)
				}
			case "read", "write", "edit":
				summary = input.FilePath
			case "grep", "glob":
				summary = input.Pattern
			}
		}
		if summary != "" {
			return fmt.Sprintf("[tool] %s: %s\n", tool, summary)
		}
		return fmt.Sprintf("[tool] %s\n", tool)
	case "step_finish":
		if msg.Part.Reason == "stop" || msg.Part.Reason == "end_turn" {
			var tokens string
			if msg.Part.Tokens != nil {
				tokens = fmt.Sprintf(", %s↑ %s↓",
					formatTokenCount(msg.Part.Tokens.Input),
					formatTokenCount(msg.Part.Tokens.Output))
			}
			if msg.Part.Cost > 0 {
				return fmt.Sprintf("[result] done ($%.4f%s)\n", msg.Part.Cost, tokens)
			}
			if tokens != "" {
				return fmt.Sprintf("[result] done (%s)\n", tokens[2:])
			}
		}
	}
	return ""
}

func (b *openCode) ParseResultEntry(line []byte) *UsageEntry {
	var msg struct {
		Type string `json:"type"`
		Part struct {
			Reason string `json:"reason"`
			Tokens *struct {
				Total  int `json:"total"`
				Input  int `json:"input"`
				Output int `json:"output"`
			} `json:"tokens"`
			Cost float64 `json:"cost"`
		} `json:"part"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}
	if msg.Type != "step_finish" {
		return nil
	}
	// Only count final step_finish events (reason "stop" or "end_turn")
	if msg.Part.Reason != "stop" && msg.Part.Reason != "end_turn" {
		return nil
	}
	u := &UsageEntry{CostUSD: msg.Part.Cost}
	if msg.Part.Tokens != nil {
		u.InputTokens = msg.Part.Tokens.Input
		u.OutputTokens = msg.Part.Tokens.Output
	}
	return u
}
