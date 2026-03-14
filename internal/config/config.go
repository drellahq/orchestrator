package config

import (
	"fmt"
	"os"
	"path"

	"github.com/goccy/go-yaml"
)

type Config struct {
	SlackBotToken string   `yaml:"slack_bot_token"`
	SlackChannel  string   `yaml:"slack_channel"`
	OutputDir     string   `yaml:"output_dir"`
	GjollEnv      string   `yaml:"gjoll_env"`
	AllowedRepos  []string `yaml:"allowed_repos"`
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

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if cfg.OutputDir == "" {
		cfg.OutputDir = "./tasks"
	}
	if cfg.GjollEnv == "" {
		cfg.GjollEnv = "./configs/sandbox.tf"
	}

	return cfg, nil
}
