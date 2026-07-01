package agent

import (
	"fmt"

	"github.com/drellahq/orchestrator/internal/shellutil"
)

// Options configures an agent backend instance.
type Options struct {
	// LLMBaseURL points the agent at a local OpenAI/Anthropic-compatible API
	// (e.g. LM Studio). When empty, the agent uses cloud credentials.
	LLMBaseURL string
	// LLMModel is the model ID on the local API (used as local/<model> in OpenCode).
	LLMModel string
}

func llmEnvBlock(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	return fmt.Sprintf(
		"export ANTHROPIC_BASE_URL=%s\nexport ANTHROPIC_API_KEY=lm-studio\n",
		shellutil.Quote(baseURL),
	)
}
