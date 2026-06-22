package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGetDefault(t *testing.T) {
	OrchestratorCommit = ""
	OSReleasePaths = []string{"/nonexistent"}

	info := Get()
	if _, ok := info.Components["orchestrator"]; !ok {
		t.Fatal("expected orchestrator component")
	}
	if _, ok := info.Components["drellaos"]; ok {
		t.Error("drellaos should not appear when os-release is missing")
	}
}

func TestGetWithBuildTimeCommit(t *testing.T) {
	OrchestratorCommit = "abc1234"
	t.Cleanup(func() { OrchestratorCommit = "" })

	dir := t.TempDir()
	osRelease := filepath.Join(dir, "os-release")
	content := "IMAGE_ID=drellaos\nBUILD_ID=def5678\nIMAGE_VERSION=20260601T120000Z\n"
	if err := os.WriteFile(osRelease, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	OSReleasePaths = []string{osRelease}

	info := Get()
	if info.Components["orchestrator"].Commit != "abc1234" {
		t.Errorf("orchestrator commit = %q, want abc1234", info.Components["orchestrator"].Commit)
	}
	if info.Components["orchestrator"].Repo != "drellahq/orchestrator" {
		t.Errorf("orchestrator repo = %q, want drellahq/orchestrator", info.Components["orchestrator"].Repo)
	}
	if info.Components["drellaos"].Commit != "def5678" {
		t.Errorf("drellaos commit = %q, want def5678", info.Components["drellaos"].Commit)
	}
	if info.Components["drellaos"].Version != "20260601T120000Z" {
		t.Errorf("drellaos version = %q, want 20260601T120000Z", info.Components["drellaos"].Version)
	}
	if info.Components["drellaos"].Repo != "drellabot/drellaos" {
		t.Errorf("drellaos repo = %q, want drellabot/drellaos", info.Components["drellaos"].Repo)
	}
}

func TestGetNonDrellaOS(t *testing.T) {
	OrchestratorCommit = "abc1234"
	t.Cleanup(func() { OrchestratorCommit = "" })

	dir := t.TempDir()
	osRelease := filepath.Join(dir, "os-release")
	content := "ID=fedora\nBUILD_ID=44.20260501.0\n"
	if err := os.WriteFile(osRelease, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	OSReleasePaths = []string{osRelease}

	info := Get()
	if _, ok := info.Components["drellaos"]; ok {
		t.Error("drellaos should not appear on non-drellaos system")
	}
}

func TestParseOSRelease(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "os-release")
	content := `NAME="Fedora Linux"
VERSION="44 (Forty Four)"
ID=fedora
BUILD_ID=abc123
IMAGE_ID=drellaos
IMAGE_VERSION=20260601T120000Z
# comment line
`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	rel := ParseOSRelease([]string{f})
	if rel["NAME"] != "Fedora Linux" {
		t.Errorf("NAME = %q, want Fedora Linux", rel["NAME"])
	}
	if rel["ID"] != "fedora" {
		t.Errorf("ID = %q, want fedora", rel["ID"])
	}
	if rel["BUILD_ID"] != "abc123" {
		t.Errorf("BUILD_ID = %q, want abc123", rel["BUILD_ID"])
	}
	if rel["IMAGE_ID"] != "drellaos" {
		t.Errorf("IMAGE_ID = %q, want drellaos", rel["IMAGE_ID"])
	}
	if rel["IMAGE_VERSION"] != "20260601T120000Z" {
		t.Errorf("IMAGE_VERSION = %q, want 20260601T120000Z", rel["IMAGE_VERSION"])
	}
}

func TestParseOSReleaseFallback(t *testing.T) {
	dir := t.TempDir()
	second := filepath.Join(dir, "os-release-2")
	if err := os.WriteFile(second, []byte("ID=test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rel := ParseOSRelease([]string{"/nonexistent", second})
	if rel["ID"] != "test" {
		t.Errorf("ID = %q, want test", rel["ID"])
	}
}

func TestParseOSReleaseMissing(t *testing.T) {
	rel := ParseOSRelease([]string{"/nonexistent"})
	if rel == nil {
		t.Error("expected non-nil map for missing os-release")
	}
	if len(rel) != 0 {
		t.Errorf("expected empty map, got %v", rel)
	}
}

func TestJSON(t *testing.T) {
	OrchestratorCommit = "abc1234"
	t.Cleanup(func() { OrchestratorCommit = "" })

	dir := t.TempDir()
	osRelease := filepath.Join(dir, "os-release")
	content := "IMAGE_ID=drellaos\nBUILD_ID=def5678\nIMAGE_VERSION=20260601T120000Z\n"
	if err := os.WriteFile(osRelease, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	OSReleasePaths = []string{osRelease}

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
	if parsed.Components["drellaos"].Version != "20260601T120000Z" {
		t.Errorf("parsed drellaos version = %q, want 20260601T120000Z", parsed.Components["drellaos"].Version)
	}
}

func TestWriteFile(t *testing.T) {
	OrchestratorCommit = "abc1234"
	OSReleasePaths = []string{"/nonexistent"}
	t.Cleanup(func() { OrchestratorCommit = "" })

	dir := t.TempDir()
	outFile := filepath.Join(dir, "version.json")

	info := Get()
	if err := info.WriteFile(outFile); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}

	var parsed Info
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if parsed.Components["orchestrator"].Commit != "abc1234" {
		t.Errorf("orchestrator = %q, want abc1234", parsed.Components["orchestrator"].Commit)
	}
}
