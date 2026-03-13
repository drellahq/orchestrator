package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		writeFile bool
		want      Config
		wantErr   bool
	}{
		{
			name:      "full config",
			writeFile: true,
			yaml:      "slack_webhook: https://hooks.slack.com/test\noutput_dir: /tmp/tasks\ngjoll_env: /path/to/sandbox.tf\n",
			want: Config{
				SlackWebhook: "https://hooks.slack.com/test",
				OutputDir:    "/tmp/tasks",
				GjollEnv:     "/path/to/sandbox.tf",
			},
		},
		{
			name:      "defaults applied",
			writeFile: true,
			yaml:      "slack_webhook: https://hooks.slack.com/test\n",
			want: Config{
				SlackWebhook: "https://hooks.slack.com/test",
				OutputDir:    "./tasks",
				GjollEnv:     "./configs/sandbox.tf",
			},
		},
		{
			name:      "empty file uses all defaults",
			writeFile: true,
			yaml:      "",
			want: Config{
				OutputDir: "./tasks",
				GjollEnv:  "./configs/sandbox.tf",
			},
		},
		{
			name:      "invalid yaml",
			writeFile: true,
			yaml:      "{{invalid",
			wantErr:   true,
		},
		{
			name:      "missing file",
			writeFile: false,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if tt.writeFile {
				if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := Load(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *got != tt.want {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}
