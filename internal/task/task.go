package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Metadata holds task metadata persisted to disk.
type Metadata struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Dir represents a per-task directory structure.
type Dir struct {
	root string
}

// Create creates a new task directory structure under outputDir.
// It fails if the task directory already exists.
func Create(outputDir, taskName string) (*Dir, error) {
	root := filepath.Join(outputDir, taskName)
	if _, err := os.Stat(root); err == nil {
		return nil, fmt.Errorf("task directory already exists: %s", root)
	}

	for _, sub := range []string{"repo", "conversations"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0755); err != nil {
			return nil, fmt.Errorf("creating task directory: %w", err)
		}
	}

	return &Dir{root: root}, nil
}

// RepoPath returns the path to the repo subdirectory.
func (d *Dir) RepoPath() string {
	return filepath.Join(d.root, "repo")
}

// ConversationsPath returns the path to the conversations subdirectory.
func (d *Dir) ConversationsPath() string {
	return filepath.Join(d.root, "conversations")
}

// SaveMetadata writes task metadata to metadata.json.
func (d *Dir) SaveMetadata(m Metadata) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling metadata: %w", err)
	}
	return os.WriteFile(filepath.Join(d.root, "metadata.json"), data, 0644)
}
