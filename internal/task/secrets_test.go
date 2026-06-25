package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveSecret(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "secret-test")
	if err != nil {
		t.Fatal(err)
	}

	if err := td.SaveSecret("rhel_activation_key", "my-key-123"); err != nil {
		t.Fatalf("SaveSecret() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "secret-test", "state_secrets.json"))
	if err != nil {
		t.Fatalf("reading state_secrets.json: %v", err)
	}

	var secrets map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		t.Fatalf("unmarshaling state_secrets.json: %v", err)
	}
	if secrets["rhel_activation_key"] != "my-key-123" {
		t.Errorf("rhel_activation_key = %q, want %q", secrets["rhel_activation_key"], "my-key-123")
	}
}

func TestSaveSecretFilePermissions(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "perm-test")
	if err != nil {
		t.Fatal(err)
	}

	if err := td.SaveSecret("key", "value"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(outputDir, "perm-test", "state_secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestSaveSecretMergesExisting(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "merge-secret")
	if err != nil {
		t.Fatal(err)
	}

	if err := td.SaveSecret("key_a", "val_a"); err != nil {
		t.Fatal(err)
	}
	if err := td.SaveSecret("key_b", "val_b"); err != nil {
		t.Fatal(err)
	}

	secrets, err := td.LoadSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if secrets["key_a"] != "val_a" {
		t.Errorf("key_a = %q, want %q", secrets["key_a"], "val_a")
	}
	if secrets["key_b"] != "val_b" {
		t.Errorf("key_b = %q, want %q", secrets["key_b"], "val_b")
	}
}

func TestLoadSecrets_NoFile(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "no-secrets")
	if err != nil {
		t.Fatal(err)
	}

	secrets, err := td.LoadSecrets()
	if err != nil {
		t.Fatalf("LoadSecrets() error: %v", err)
	}
	if secrets != nil {
		t.Errorf("expected nil secrets, got %v", secrets)
	}
}

func TestLoadStateMergesSecrets(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "merge-test")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("merge-test", "desc", "", now); err != nil {
		t.Fatal(err)
	}
	if err := td.SaveSecret("rhel_activation_key", "test-key-456"); err != nil {
		t.Fatal(err)
	}

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Secrets == nil {
		t.Fatal("Secrets is nil, expected populated map")
	}
	if state.Secrets["rhel_activation_key"] != "test-key-456" {
		t.Errorf("Secrets[rhel_activation_key] = %q, want %q", state.Secrets["rhel_activation_key"], "test-key-456")
	}
}

func TestLoadStateWithoutSecrets(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "no-secret-state")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("no-secret-state", "desc", "", now); err != nil {
		t.Fatal(err)
	}

	state, err := td.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Secrets != nil {
		t.Errorf("Secrets should be nil when no state_secrets.json exists, got %v", state.Secrets)
	}
}

func TestSecretsNotInStateJSON(t *testing.T) {
	outputDir := t.TempDir()
	td, err := Create(outputDir, "no-leak")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	if err := td.SaveMetadata("no-leak", "desc", "", now); err != nil {
		t.Fatal(err)
	}
	if err := td.SaveSecret("rhel_activation_key", "secret-key"); err != nil {
		t.Fatal(err)
	}

	// Trigger a state save by updating status
	if err := td.SetStatus(StatusDone); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "no-leak", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["rhel_activation_key"]; ok {
		t.Error("state.json should not contain rhel_activation_key")
	}
	if _, ok := raw["secrets"]; ok {
		t.Error("state.json should not contain secrets key")
	}
}

func TestMergeSecrets(t *testing.T) {
	stateMap := map[string]json.RawMessage{
		"name":   json.RawMessage(`"test"`),
		"status": json.RawMessage(`"done"`),
	}

	secrets := map[string]string{
		"rhel_activation_key": "key-789",
	}

	MergeSecrets(stateMap, secrets)

	if string(stateMap["rhel_activation_key"]) != `"key-789"` {
		t.Errorf("rhel_activation_key = %s, want %q", stateMap["rhel_activation_key"], "key-789")
	}
	if string(stateMap["name"]) != `"test"` {
		t.Errorf("name was modified: %s", stateMap["name"])
	}
}

func TestMergeSecretsOverwriteWarning(t *testing.T) {
	stateMap := map[string]json.RawMessage{
		"name":                json.RawMessage(`"test"`),
		"rhel_activation_key": json.RawMessage(`"old-key"`),
	}

	secrets := map[string]string{
		"rhel_activation_key": "new-key",
	}

	// MergeSecrets logs a warning; we just verify it doesn't panic
	// and the value is overwritten.
	MergeSecrets(stateMap, secrets)

	if string(stateMap["rhel_activation_key"]) != `"new-key"` {
		t.Errorf("rhel_activation_key = %s, want %q", stateMap["rhel_activation_key"], "new-key")
	}
}
