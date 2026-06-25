package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

func (d *Dir) secretsPath() string {
	return filepath.Join(d.root, "state_secrets.json")
}

// LoadSecrets reads the per-task secrets from state_secrets.json.
// Returns an empty map if the file does not exist.
func (d *Dir) LoadSecrets() (map[string]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loadSecretsLocked()
}

func (d *Dir) loadSecretsLocked() (map[string]string, error) {
	data, err := os.ReadFile(d.secretsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading secrets: %w", err)
	}
	var secrets map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("unmarshaling secrets: %w", err)
	}
	return secrets, nil
}

func (d *Dir) saveSecretsLocked(secrets map[string]string) error {
	data, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling secrets: %w", err)
	}
	return os.WriteFile(d.secretsPath(), data, 0600)
}

// SaveSecret writes a single key-value pair to state_secrets.json,
// merging with any existing secrets.
func (d *Dir) SaveSecret(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	secrets, err := d.loadSecretsLocked()
	if err != nil {
		return err
	}
	if secrets == nil {
		secrets = make(map[string]string)
	}
	secrets[key] = value
	return d.saveSecretsLocked(secrets)
}

// MergeSecrets loads state_secrets.json and overlays its values onto
// the given raw JSON map. It logs a warning for each key that already
// exists in the map. The caller is responsible for unmarshaling the
// result back into a State.
func MergeSecrets(stateMap map[string]json.RawMessage, secrets map[string]string) {
	for k, v := range secrets {
		if _, exists := stateMap[k]; exists {
			slog.Warn("state_secrets.json overwriting state.json key", "key", k)
		}
		raw, _ := json.Marshal(v)
		stateMap[k] = raw
	}
}
