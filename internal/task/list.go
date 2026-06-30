package task

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Summary holds display metadata for a task directory.
type Summary struct {
	Name             string
	Status           string
	SandboxDestroyed bool
	CreatedAt        string
	OpenPRCount      int
}

// List returns summaries for task directories under outputDir.
// When includeAll is false, directories without a readable state.json are skipped.
func List(outputDir string, includeAll bool) ([]Summary, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading output dir: %w", err)
	}

	var summaries []Summary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		statePath := filepath.Join(outputDir, name, "state.json")
		if _, err := os.Stat(statePath); os.IsNotExist(err) {
			if !includeAll {
				continue
			}
			summaries = append(summaries, Summary{Name: name})
			continue
		}

		td, err := Open(outputDir, name)
		if err != nil {
			continue
		}
		state, err := td.LoadState()
		if err != nil {
			if !includeAll {
				continue
			}
			summaries = append(summaries, Summary{Name: name})
			continue
		}
		createdAt := ""
		if !state.CreatedAt.IsZero() {
			createdAt = state.CreatedAt.Format("2006-01-02 15:04")
		}
		summaries = append(summaries, Summary{
			Name:             state.Name,
			Status:           state.Status,
			SandboxDestroyed: state.SandboxDestroyed,
			CreatedAt:        createdAt,
			OpenPRCount:      state.OpenPRCount(),
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})
	return summaries, nil
}

// OpenPRCount returns the number of PRs that are not closed.
func (s *State) OpenPRCount() int {
	count := 0
	for _, pr := range s.Resources.GitHub.PRs {
		if !pr.Closed {
			count++
		}
	}
	return count
}
