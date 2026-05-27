package version

import (
	"encoding/json"
	"testing"
)

func TestGetDefault(t *testing.T) {
	OrchestratorCommit = ""
	DrellaOSCommit = ""

	info := Get()
	if _, ok := info.Components["orchestrator"]; !ok {
		t.Fatal("expected orchestrator component")
	}
	if _, ok := info.Components["drellaos"]; ok {
		t.Error("drellaos should not appear when DrellaOSCommit is empty")
	}
}

func TestGetWithCommits(t *testing.T) {
	OrchestratorCommit = "abc1234"
	DrellaOSCommit = "def5678"
	t.Cleanup(func() {
		OrchestratorCommit = ""
		DrellaOSCommit = ""
	})

	info := Get()
	if info.Components["orchestrator"].Commit != "abc1234" {
		t.Errorf("orchestrator commit = %q, want abc1234", info.Components["orchestrator"].Commit)
	}
	if info.Components["drellaos"].Commit != "def5678" {
		t.Errorf("drellaos commit = %q, want def5678", info.Components["drellaos"].Commit)
	}
}

func TestJSON(t *testing.T) {
	OrchestratorCommit = "abc1234"
	DrellaOSCommit = "def5678"
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
