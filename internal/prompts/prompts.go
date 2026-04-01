package prompts

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed on_init.md
var OnInit string

//go:embed on_pr_comment.md
var OnPRComment string

//go:embed base.md
var Base string

// LoadAgentPrompt reads an agent role prompt from the agents directory.
// The role name maps to a file named <role>.md in the directory.
func LoadAgentPrompt(agentsDir, role string) (string, error) {
	path := filepath.Join(agentsDir, role+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("loading agent prompt %q: %w", role, err)
	}
	return string(data), nil
}

// BuildSystemPrompt assembles the full system prompt for an agent session
// by concatenating the base prompt and the role-specific prompt.
func BuildSystemPrompt(base, rolePrompt string) string {
	return strings.TrimSpace(base) + "\n\n" + strings.TrimSpace(rolePrompt) + "\n"
}
