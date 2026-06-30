package agent

import (
	"encoding/json"
	"fmt"
)

type openCodeMCP struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
}

// OpenCodeConfigJSON builds opencode.json for sandbox setup.
func OpenCodeConfigJSON(baseURL, model string, mcp map[string]openCodeMCP) (string, error) {
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"permission": map[string]any{
			"bash":  "allow",
			"edit":  "allow",
			"write": "allow",
			"read": map[string]any{
				"*": "allow",
			},
		},
		"agent": map[string]any{
			"build": map[string]any{
				"permission": map[string]any{
					"bash":  "allow",
					"edit":  "allow",
					"write": "allow",
				},
			},
		},
	}
	if baseURL != "" && model != "" {
		cfg["provider"] = map[string]any{
			localLLMProviderID: map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "Local LLM (orchestrator)",
				"options": map[string]any{
					"baseURL":       baseURL,
					"apiKey":        "local",
					"timeout":       600000,
					"chunkTimeout":  60000,
				},
				"models": map[string]any{
					model: map[string]any{
						"name": model,
					},
				},
			},
		}
	}
	if len(mcp) > 0 {
		cfg["mcp"] = mcp
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (b *openCode) writeConfigCmd(mcpName, transport, mcpURL string) (string, error) {
	mcpType := "remote"
	if transport == "stdio" {
		mcpType = "local"
	}
	mcp := map[string]openCodeMCP{
		mcpName: {Type: mcpType, URL: mcpURL},
	}
	body, err := OpenCodeConfigJSON(b.llmBaseURL, b.llmModel, mcp)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`mkdir -p ~/workspace && cat > ~/workspace/opencode.json << 'OCJSONEOF'
%s
OCJSONEOF`, body), nil
}
