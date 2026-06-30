package agent

import (
	"strings"
	"testing"
)

func TestOpenCodeConfigJSON(t *testing.T) {
	raw, err := OpenCodeConfigJSON("http://127.0.0.1:11434/v1", "qwen/qwen3.5-9b", map[string]openCodeMCP{
		"orchestrator": {Type: "remote", URL: "http://localhost:19090/mcp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"local"`,
		`"http://127.0.0.1:11434/v1"`,
		`"qwen/qwen3.5-9b"`,
		`"orchestrator"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("config missing %q: %s", want, raw)
		}
	}
}

func TestResolveLocalModelConfigured(t *testing.T) {
	got, err := ResolveLocalModel("http://127.0.0.1:11434/v1", "my-model")
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-model" {
		t.Fatalf("got %q", got)
	}
}
