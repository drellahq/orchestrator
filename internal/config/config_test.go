package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
				SlackWebhook:     "https://hooks.slack.com/test",
				OutputDir:        "/tmp/tasks",
				SandboxBackend:   "gjoll",
				GjollEnv:         "/path/to/sandbox.tf",
				PodmanImage:      "fedora:43",
				AnthropicKeyFile: "~/.anthropic/api_key",
			},
		},
		{
			name:      "defaults applied",
			writeFile: true,
			yaml:      "slack_webhook: https://hooks.slack.com/test\n",
			want: Config{
				SlackWebhook:     "https://hooks.slack.com/test",
				OutputDir:        "./tasks",
				SandboxBackend:   "gjoll",
				GjollEnv:         "./configs/sandbox.tf",
				PodmanImage:      "fedora:43",
				AnthropicKeyFile: "~/.anthropic/api_key",
			},
		},
		{
			name:      "empty file uses all defaults",
			writeFile: true,
			yaml:      "",
			want: Config{
				OutputDir:        "./tasks",
				SandboxBackend:   "gjoll",
				GjollEnv:         "./configs/sandbox.tf",
				PodmanImage:      "fedora:43",
				AnthropicKeyFile: "~/.anthropic/api_key",
			},
		},
		{
			name:      "allowed_repos parsed",
			writeFile: true,
			yaml:      "allowed_repos:\n  - osbuild/osbuild\n  - drellabot/*\n",
			want: Config{
				OutputDir:        "./tasks",
				SandboxBackend:   "gjoll",
				GjollEnv:         "./configs/sandbox.tf",
				PodmanImage:      "fedora:43",
				AnthropicKeyFile: "~/.anthropic/api_key",
				AllowedRepos:     []string{"osbuild/osbuild", "drellabot/*"},
			},
		},
		{
			name:      "daemon config parsed",
			writeFile: true,
			yaml:      "daemon:\n  poll_interval: \"30s\"\n  allowed_commenters:\n    - alice\n    - bob\n",
			want: Config{
				OutputDir:        "./tasks",
				SandboxBackend:   "gjoll",
				GjollEnv:         "./configs/sandbox.tf",
				PodmanImage:      "fedora:43",
				AnthropicKeyFile: "~/.anthropic/api_key",
				Daemon: DaemonConfig{
					PollInterval:      "30s",
					AllowedCommenters: []string{"alice", "bob"},
				},
			},
		},
		{
			name:      "daemon config with orgs parsed",
			writeFile: true,
			yaml:      "daemon:\n  poll_interval: \"30s\"\n  allowed_commenters:\n    - alice\n  allowed_commenters_orgs:\n    drellahq: maintainer\n    otherorg: owner\n",
			want: Config{
				OutputDir:        "./tasks",
				SandboxBackend:   "gjoll",
				GjollEnv:         "./configs/sandbox.tf",
				PodmanImage:      "fedora:43",
				AnthropicKeyFile: "~/.anthropic/api_key",
				Daemon: DaemonConfig{
					PollInterval:      "30s",
					AllowedCommenters: []string{"alice"},
					AllowedCommentersOrgs: map[string]string{
						"drellahq": "maintainer",
						"otherorg": "owner",
					},
				},
			},
		},
		{
			name:      "profiles config parsed",
			writeFile: true,
			yaml:      "profiles_repo: drellabot/profiles\nprofiles_dir: /tmp/profiles\n",
			want: Config{
				OutputDir:        "./tasks",
				SandboxBackend:   "gjoll",
				GjollEnv:         "./configs/sandbox.tf",
				PodmanImage:      "fedora:43",
				AnthropicKeyFile: "~/.anthropic/api_key",
				ProfilesRepo:     "drellabot/profiles",
				ProfilesDir:      "/tmp/profiles",
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
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestRepoAllowed(t *testing.T) {
	tests := []struct {
		name         string
		allowedRepos []string
		repo         string
		want         bool
	}{
		{
			name:         "exact match",
			allowedRepos: []string{"osbuild/osbuild"},
			repo:         "osbuild/osbuild",
			want:         true,
		},
		{
			name:         "wildcard match",
			allowedRepos: []string{"drellahq/*"},
			repo:         "drellahq/orchestrator",
			want:         true,
		},
		{
			name:         "no match",
			allowedRepos: []string{"osbuild/osbuild"},
			repo:         "evil/repo",
			want:         false,
		},
		{
			name:         "empty list denies all",
			allowedRepos: nil,
			repo:         "osbuild/osbuild",
			want:         false,
		},
		{
			name:         "wildcard does not cross slash",
			allowedRepos: []string{"org/*"},
			repo:         "org/a/b",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{AllowedRepos: tt.allowedRepos}
			if got := cfg.RepoAllowed(tt.repo); got != tt.want {
				t.Errorf("RepoAllowed(%q) = %v, want %v", tt.repo, got, tt.want)
			}
		})
	}
}

// mockLister implements OrgMemberLister for testing.
type mockLister struct {
	members map[string]map[string][]string // org -> role -> members
}

func (m *mockLister) ListOrgMembers(_ context.Context, org, role string) ([]string, error) {
	if roles, ok := m.members[org]; ok {
		if members, ok := roles[role]; ok {
			return members, nil
		}
	}
	return nil, fmt.Errorf("org %s not found", org)
}

func TestResolveAllowedCommenters(t *testing.T) {
	tests := []struct {
		name       string
		cfg        DaemonConfig
		members    map[string]map[string][]string
		wantMerged []string
	}{
		{
			name: "static only",
			cfg: DaemonConfig{
				AllowedCommenters: []string{"alice", "bob"},
			},
			wantMerged: []string{"alice", "bob"},
		},
		{
			name: "orgs only",
			cfg: DaemonConfig{
				AllowedCommentersOrgs: map[string]string{
					"drellahq": "maintainer",
				},
			},
			members: map[string]map[string][]string{
				"drellahq": {"maintainer": {"alice", "bob", "charlie"}},
			},
			wantMerged: []string{"alice", "bob", "charlie"},
		},
		{
			name: "static and orgs merged and deduped",
			cfg: DaemonConfig{
				AllowedCommenters: []string{"alice", "dave"},
				AllowedCommentersOrgs: map[string]string{
					"drellahq": "maintainer",
				},
			},
			members: map[string]map[string][]string{
				"drellahq": {"maintainer": {"alice", "bob", "charlie"}},
			},
			wantMerged: []string{"alice", "bob", "charlie", "dave"},
		},
		{
			name: "multiple orgs",
			cfg: DaemonConfig{
				AllowedCommenters: []string{"alice"},
				AllowedCommentersOrgs: map[string]string{
					"org1": "maintainer",
					"org2": "owner",
				},
			},
			members: map[string]map[string][]string{
				"org1": {"maintainer": {"bob", "charlie"}},
				"org2": {"owner": {"dave"}},
			},
			wantMerged: []string{"alice", "bob", "charlie", "dave"},
		},
		{
			name: "failing org is skipped",
			cfg: DaemonConfig{
				AllowedCommenters: []string{"alice"},
				AllowedCommentersOrgs: map[string]string{
					"badorg": "maintainer",
				},
			},
			members:    map[string]map[string][]string{},
			wantMerged: []string{"alice"},
		},
		{
			name:       "empty config",
			cfg:        DaemonConfig{},
			wantMerged: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := &mockLister{members: tt.members}
			resolved, err := ResolveAllowedCommenters(context.Background(), &tt.cfg, lister)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := resolved.Merged
			sort.Strings(got)
			want := tt.wantMerged
			sort.Strings(want)

			if len(got) != len(want) {
				t.Fatalf("merged = %v, want %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("merged[%d] = %q, want %q", i, got[i], want[i])
				}
			}
		})
	}
}

func TestWriteResolvedCommenters(t *testing.T) {
	dir := t.TempDir()
	resolved := &ResolvedCommenters{
		Static:   []string{"alice"},
		OrgUsers: map[string][]string{"drellahq": {"bob", "charlie"}},
		Merged:   []string{"alice", "bob", "charlie"},
	}

	if err := WriteResolvedCommenters(dir, resolved); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "allowed_commenters_resolved.yaml"))
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	content := string(data)
	if len(content) == 0 {
		t.Fatal("file is empty")
	}
	for _, want := range []string{"alice", "bob", "charlie", "drellahq"} {
		if !strings.Contains(content, want) {
			t.Errorf("file does not contain %q", want)
		}
	}
}
