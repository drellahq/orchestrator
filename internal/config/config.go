package config

import (
	"fmt"
	"os"
	"path"

	"github.com/goccy/go-yaml"
)

// DaemonConfig holds settings for the daemon polling loop.
type DaemonConfig struct {
	PollInterval      string   `yaml:"poll_interval"`
	AllowedCommenters []string `yaml:"allowed_commenters"`
	TasksRepo         string   `yaml:"tasks_repo"`
}

// HandoffConfig controls what context a pipeline step receives from the
// prior step's output.
type HandoffConfig struct {
	IncludeDiff            *bool `yaml:"include_diff"`
	IncludePriorTranscript *bool `yaml:"include_prior_transcript"`
	IncludePriorSummary    *bool `yaml:"include_prior_summary"`
}

// IncludeDiffOrDefault returns the include_diff value, defaulting to true.
func (h HandoffConfig) IncludeDiffOrDefault() bool {
	if h.IncludeDiff != nil {
		return *h.IncludeDiff
	}
	return true
}

// IncludePriorTranscriptOrDefault returns the include_prior_transcript value, defaulting to false.
func (h HandoffConfig) IncludePriorTranscriptOrDefault() bool {
	if h.IncludePriorTranscript != nil {
		return *h.IncludePriorTranscript
	}
	return false
}

// IncludePriorSummaryOrDefault returns the include_prior_summary value, defaulting to false.
func (h HandoffConfig) IncludePriorSummaryOrDefault() bool {
	if h.IncludePriorSummary != nil {
		return *h.IncludePriorSummary
	}
	return false
}

// PipelineStep defines one step in a pipeline.
type PipelineStep struct {
	Role          string        `yaml:"role"`
	MaxIterations int           `yaml:"max_iterations"`
	Handoff       HandoffConfig `yaml:"handoff"`
}

type Config struct {
	SlackWebhook string                      `yaml:"slack_webhook"`
	OutputDir    string                      `yaml:"output_dir"`
	GjollEnv     string                      `yaml:"gjoll_env"`
	AllowedRepos []string                    `yaml:"allowed_repos"`
	Daemon       DaemonConfig                `yaml:"daemon"`
	AgentsDir    string                      `yaml:"agents_dir"`
	Pipelines    map[string][]PipelineStep   `yaml:"pipelines"`
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

// Pipeline returns the named pipeline, or the "default" pipeline if name
// is empty. If no pipelines are configured, it returns a single-step
// pipeline with the "producer" role for backward compatibility.
func (c *Config) Pipeline(name string) []PipelineStep {
	if name == "" {
		name = "default"
	}
	if steps, ok := c.Pipelines[name]; ok {
		return steps
	}
	// Backward-compatible: single producer step.
	return []PipelineStep{{Role: "producer", MaxIterations: 1}}
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
	if cfg.AgentsDir == "" {
		cfg.AgentsDir = "./agents"
	}

	// Apply defaults for pipeline steps.
	for name, steps := range cfg.Pipelines {
		for i := range steps {
			if steps[i].MaxIterations == 0 {
				steps[i].MaxIterations = 3
			}
		}
		cfg.Pipelines[name] = steps
	}

	return cfg, nil
}
