package config

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

type Config struct {
	SlackWebhook string `yaml:"slack_webhook"`
	OutputDir    string `yaml:"output_dir"`
	GjollEnv     string `yaml:"gjoll_env"`
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
