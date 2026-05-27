package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellabot/orchestrator/internal/version"
)

func TestVersionText(t *testing.T) {
	version.OrchestratorCommit = "abc1234"
	t.Cleanup(func() { version.OrchestratorCommit = "" })

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version command error: %v", err)
	}
	if !strings.Contains(buf.String(), "orchestrator: abc1234") {
		t.Errorf("output = %q, want orchestrator: abc1234", buf.String())
	}
}

func TestVersionJSON(t *testing.T) {
	version.OrchestratorCommit = "abc1234"
	version.DrellaOSCommit = "def5678"
	t.Cleanup(func() {
		version.OrchestratorCommit = ""
		version.DrellaOSCommit = ""
	})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"version", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version --json error: %v", err)
	}

	var info version.Info
	if err := json.Unmarshal(buf.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal error: %v\noutput: %s", err, buf.String())
	}
	if info.Components["orchestrator"].Commit != "abc1234" {
		t.Errorf("orchestrator = %q, want abc1234", info.Components["orchestrator"].Commit)
	}
	if info.Components["drellaos"].Commit != "def5678" {
		t.Errorf("drellaos = %q, want def5678", info.Components["drellaos"].Commit)
	}
}

func TestVersionOutput(t *testing.T) {
	version.OrchestratorCommit = "abc1234"
	t.Cleanup(func() { version.OrchestratorCommit = "" })

	dir := t.TempDir()
	outFile := filepath.Join(dir, "version.json")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"version", "-o", outFile})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version -o error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}

	var info version.Info
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if info.Components["orchestrator"].Commit != "abc1234" {
		t.Errorf("orchestrator = %q, want abc1234", info.Components["orchestrator"].Commit)
	}
}
