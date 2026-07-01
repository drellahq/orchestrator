package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const localLLMProviderID = "local"

// ResolveLocalModel returns the configured model ID or the first model from a
// local OpenAI-compatible /v1/models endpoint.
func ResolveLocalModel(baseURL, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching models from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("models endpoint %s returned HTTP %d", url, resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decoding models response: %w", err)
	}
	for _, m := range payload.Data {
		if m.ID != "" {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("no models returned from %s", url)
}

// LocalLLMProviderID returns the OpenCode provider key for orchestrator-managed local LLMs.
func LocalLLMProviderID() string {
	return localLLMProviderID
}
