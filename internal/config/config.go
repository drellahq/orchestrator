package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sort"

	"github.com/goccy/go-yaml"
)

// DaemonConfig holds settings for the daemon polling loop.
type DaemonConfig struct {
	PollInterval      string            `yaml:"poll_interval"`
	AllowedCommenters []string          `yaml:"allowed_commenters"`
	AllowedCommentersOrgs map[string]string `yaml:"allowed_commenters_orgs"`
	TasksRepo         string            `yaml:"tasks_repo"`
}

type Config struct {
	SlackWebhook string       `yaml:"slack_webhook"`
	OutputDir    string       `yaml:"output_dir"`
	BaseURL      string       `yaml:"base_url"`
	
	// Sandbox backend: "gjoll" (VMs) or "podman" (containers)
	SandboxBackend string `yaml:"sandbox_backend"`
	
	// Gjoll backend settings
	GjollEnv     string   `yaml:"gjoll_env"`      // path to .tf file for VM provisioning
	
	// Podman backend settings  
	PodmanImage      string `yaml:"podman_image"`       // container image (e.g. "fedora:43")
	AnthropicKeyFile string `yaml:"anthropic_key_file"` // path to API key for mounting
	
	AllowedRepos []string     `yaml:"allowed_repos"`
	ProfilesRepo string       `yaml:"profiles_repo"`
	ProfilesDir  string       `yaml:"profiles_dir"`
	Daemon       DaemonConfig `yaml:"daemon"`
}

// RepoAllowed reports whether repo is permitted by the AllowedRepos allowlist.
// Each entry may contain wildcards understood by path.Match (e.g. "org/*").
// An empty list denies all repos.
func (c *Config) RepoAllowed(repo string) bool {
	for _, pattern := range c.AllowedRepos {
		if matched, _ := path.Match(pattern, repo); matched {
			return true
		}
	}
	return false
}

// OrgMemberLister can list members of a GitHub organization.
type OrgMemberLister interface {
	ListOrgMembers(ctx context.Context, org, role string) ([]string, error)
}

// ResolvedCommenters holds the resolved allowed commenters list and metadata
// about where each entry came from.
type ResolvedCommenters struct {
	Static   []string          `yaml:"static"`
	OrgUsers map[string][]string `yaml:"org_users"`
	Merged   []string          `yaml:"merged"`
}

// ResolveAllowedCommenters merges the static allowed_commenters list with
// members fetched from allowed_commenters_orgs. It returns the merged
// deduplicated list and the full resolution details.
func ResolveAllowedCommenters(ctx context.Context, cfg *DaemonConfig, lister OrgMemberLister) (*ResolvedCommenters, error) {
	resolved := &ResolvedCommenters{
		Static:   cfg.AllowedCommenters,
		OrgUsers: make(map[string][]string),
	}

	seen := make(map[string]bool)
	for _, u := range cfg.AllowedCommenters {
		seen[u] = true
	}

	for org, role := range cfg.AllowedCommentersOrgs {
		members, err := lister.ListOrgMembers(ctx, org, role)
		if err != nil {
			slog.Warn("Failed to resolve org members, skipping", "org", org, "role", role, "error", err)
			continue
		}
		resolved.OrgUsers[org] = members
		for _, m := range members {
			seen[m] = true
		}
	}

	for u := range seen {
		resolved.Merged = append(resolved.Merged, u)
	}
	sort.Strings(resolved.Merged)

	return resolved, nil
}

// WriteResolvedCommenters writes the resolved commenters to a YAML file
// in the output directory for dashboard visibility.
func WriteResolvedCommenters(outputDir string, resolved *ResolvedCommenters) error {
	data, err := yaml.Marshal(resolved)
	if err != nil {
		return fmt.Errorf("marshaling resolved commenters: %w", err)
	}
	return os.WriteFile(path.Join(outputDir, "allowed_commenters_resolved.yaml"), data, 0644)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Defaults
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./tasks"
	}
	if cfg.SandboxBackend == "" {
		cfg.SandboxBackend = "gjoll"  // default to gjoll for backward compat
	}
	if cfg.GjollEnv == "" {
		cfg.GjollEnv = "./configs/sandbox.tf"
	}
	if cfg.PodmanImage == "" {
		cfg.PodmanImage = "fedora:43"
	}
	if cfg.AnthropicKeyFile == "" {
		cfg.AnthropicKeyFile = "~/.anthropic/api_key"
	}

	return cfg, nil
}
