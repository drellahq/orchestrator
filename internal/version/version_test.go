package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGetDefault(t *testing.T) {
	OrchestratorCommit = ""
	DrellaOSCommit = ""
	DrellaOSCommitFile = "/nonexistent"

	info := Get()
	if _, ok := info.Components["orchestrator"]; !ok {
		t.Fatal("expected orchestrator component")
	}
	if _, ok := info.Components["drellaos"]; ok {
		t.Error("drellaos should not appear when commit is empty and file is missing")
	}
}

func TestGetWithBuildTimeCommit(t *testing.T) {
	OrchestratorCommit = "abc1234"
	DrellaOSCommit = "def5678"
	DrellaOSCommitFile = "/nonexistent"
	t.Cleanup(func() {
		OrchestratorCommit = ""
		DrellaOSCommit = ""
	})

	info := Get()
	if info.Components["orchestrator"].Commit != "abc1234" {
		t.Errorf("orchestrator commit = %q, want abc1234", info.Components["orchestrator"].Commit)
	}
	if info.Components["orchestrator"].Repo != "drellabot/orchestrator" {
		t.Errorf("orchestrator repo = %q, want drellabot/orchestrator", info.Components["orchestrator"].Repo)
	}
	if info.Components["drellaos"].Commit != "def5678" {
		t.Errorf("drellaos commit = %q, want def5678", info.Components["drellaos"].Commit)
	}
	if info.Components["drellaos"].Repo != "drellabot/drellaos" {
		t.Errorf("drellaos repo = %q, want drellabot/drellaos", info.Components["drellaos"].Repo)
	}
}

func TestGetDrellaOSFromFile(t *testing.T) {
	OrchestratorCommit = "abc1234"
	DrellaOSCommit = ""
	t.Cleanup(func() {
		OrchestratorCommit = ""
	})

	dir := t.TempDir()
	commitFile := filepath.Join(dir, "drellaos-commit")
	if err := os.WriteFile(commitFile, []byte("file789\n"), 0644); err != nil {
		t.Fatal(err)
	}
	DrellaOSCommitFile = commitFile

	info := Get()
	if info.Components["drellaos"].Commit != "file789" {
		t.Errorf("drellaos commit = %q, want file789", info.Components["drellaos"].Commit)
	}
}

func TestGetDrellaOSBuildTimeOverridesFile(t *testing.T) {
	DrellaOSCommit = "buildtime"
	t.Cleanup(func() {
		DrellaOSCommit = ""
	})

	dir := t.TempDir()
	commitFile := filepath.Join(dir, "drellaos-commit")
	if err := os.WriteFile(commitFile, []byte("fromfile\n"), 0644); err != nil {
		t.Fatal(err)
	}
	DrellaOSCommitFile = commitFile

	info := Get()
	if info.Components["drellaos"].Commit != "buildtime" {
		t.Errorf("drellaos commit = %q, want buildtime (build-time should override file)", info.Components["drellaos"].Commit)
	}
}

func TestJSON(t *testing.T) {
	OrchestratorCommit = "abc1234"
	DrellaOSCommit = "def5678"
	DrellaOSCommitFile = "/nonexistent"
	t.Cleanup(func() {
		OrchestratorCommit = ""
		DrellaOSCommit = ""
	})

	info := Get()
	data, err := info.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	var parsed Info
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if parsed.Components["orchestrator"].Commit != "abc1234" {
		t.Errorf("parsed orchestrator commit = %q, want abc1234", parsed.Components["orchestrator"].Commit)
	}
	if parsed.Components["drellaos"].Commit != "def5678" {
		t.Errorf("parsed drellaos commit = %q, want def5678", parsed.Components["drellaos"].Commit)
	}
}

func TestJSONExtensible(t *testing.T) {
	OrchestratorCommit = "abc1234"
	DrellaOSCommit = ""
	DrellaOSCommitFile = "/nonexistent"
	t.Cleanup(func() {
		OrchestratorCommit = ""
	})

	info := Get()
	info.Components["custom-service"] = Component{Commit: "cust123"}

	data, err := info.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	var parsed Info
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if parsed.Components["custom-service"].Commit != "cust123" {
		t.Errorf("custom-service commit = %q, want cust123", parsed.Components["custom-service"].Commit)
	}
}
