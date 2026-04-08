package profile

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

// Profile holds the parsed contents of a profile directory.
type Profile struct {
	Name     string
	Claudemd string // Required — profile-specific CLAUDE.md content
	Setup    string // Optional — path to setup.sh on the host
	MCP      *MCPConfig // Optional — parsed mcp.yaml
	Settings string // Optional — path to settings.json on the host
}

// MCPConfig represents the parsed mcp.yaml file.
type MCPConfig struct {
	Servers []MCPServer `yaml:"servers"`
}

// MCPServer represents a single MCP server entry.
type MCPServer struct {
	Name      string   `yaml:"name"`
	Transport string   `yaml:"transport"` // "stdio" or "http"
	Command   string   `yaml:"command"`   // stdio: command to run
	Args      []string `yaml:"args"`      // stdio: command arguments
	URL       string   `yaml:"url"`       // http: server URL
	Scope     string   `yaml:"scope"`     // optional scope (e.g. "user")
}

// Load reads and validates a profile from a directory.
// source is the root profiles directory; name is the profile subdirectory.
func Load(source, name string) (*Profile, error) {
	dir := filepath.Join(source, name)

	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("profile %q not found in %s: %w", name, source, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("profile %q is not a directory", name)
	}

	// CLAUDE.md is required
	claudemdPath := filepath.Join(dir, "CLAUDE.md")
	claudemd, err := os.ReadFile(claudemdPath)
	if err != nil {
		return nil, fmt.Errorf("profile %q missing required CLAUDE.md: %w", name, err)
	}

	p := &Profile{
		Name:     name,
		Claudemd: string(claudemd),
	}

	// setup.sh is optional
	setupPath := filepath.Join(dir, "setup.sh")
	if _, err := os.Stat(setupPath); err == nil {
		p.Setup = setupPath
	}

	// mcp.yaml is optional
	mcpPath := filepath.Join(dir, "mcp.yaml")
	if mcpData, err := os.ReadFile(mcpPath); err == nil {
		var mcpCfg MCPConfig
		if err := yaml.Unmarshal(mcpData, &mcpCfg); err != nil {
			return nil, fmt.Errorf("profile %q: parsing mcp.yaml: %w", name, err)
		}
		if err := validateMCP(&mcpCfg); err != nil {
			return nil, fmt.Errorf("profile %q: invalid mcp.yaml: %w", name, err)
		}
		p.MCP = &mcpCfg
	}

	// settings.json is optional
	settingsPath := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		p.Settings = settingsPath
	}

	return p, nil
}

func validateMCP(cfg *MCPConfig) error {
	for i, s := range cfg.Servers {
		if s.Name == "" {
			return fmt.Errorf("server[%d]: name is required", i)
		}
		switch s.Transport {
		case "stdio":
			if s.Command == "" {
				return fmt.Errorf("server[%d] %q: command is required for stdio transport", i, s.Name)
			}
		case "http":
			if s.URL == "" {
				return fmt.Errorf("server[%d] %q: url is required for http transport", i, s.Name)
			}
		default:
			return fmt.Errorf("server[%d] %q: unsupported transport %q (must be stdio or http)", i, s.Name, s.Transport)
		}
	}
	return nil
}
