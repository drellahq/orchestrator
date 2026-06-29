package gjoll

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		bin     string
		wantBin string
	}{
		{
			name:    "default binary",
			bin:     "",
			wantBin: "gjoll",
		},
		{
			name:    "custom binary",
			bin:     "/usr/local/bin/gjoll",
			wantBin: "/usr/local/bin/gjoll",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(tt.bin)
			if r.bin != tt.wantBin {
				t.Errorf("bin = %q, want %q", r.bin, tt.wantBin)
			}
		})
	}
}

// writeArgCapture creates a shell script that writes its arguments to a file,
// one per line. Returns the script path and the output file path.
func writeArgCapture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "capture")
	outFile := filepath.Join(dir, "args.txt")

	content := "#!/bin/sh\nprintf '%s\n' \"$@\" > " + outFile + "\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script, outFile
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading captured args: %v", err)
	}
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestUpEnvVars(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")

	script := filepath.Join(dir, "capture-env")
	content := "#!/bin/sh\nenv > " + envFile + "\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	r := New(script)
	env := []string{
		"TF_VAR_rhel_org_id=org-123",
		"TF_VAR_rhel_activation_key=key-abc",
	}
	if err := r.Up(context.Background(), "test", "/tmp/env.tf", env); err != nil {
		t.Fatalf("Up with env: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}
	envStr := string(data)
	for _, want := range env {
		if !strings.Contains(envStr, want) {
			t.Errorf("env missing %q", want)
		}
	}
}

func TestCommandConstruction(t *testing.T) {
	// Skip if /bin/sh is not available (unlikely but safe)
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	tests := []struct {
		name     string
		call     func(r *Runner, ctx context.Context) error
		wantArgs []string
	}{
		{
			name: "Up",
			call: func(r *Runner, ctx context.Context) error {
				return r.Up(ctx, "my-sandbox", "/path/to/env.tf", nil)
			},
			wantArgs: []string{"up", "-n", "my-sandbox", "/path/to/env.tf"},
		},
		{
			name: "Start",
			call: func(r *Runner, ctx context.Context) error {
				return r.Start(ctx, "my-sandbox")
			},
			wantArgs: []string{"start", "my-sandbox"},
		},
		{
			name: "SSH single command string",
			call: func(r *Runner, ctx context.Context) error {
				return r.SSH(ctx, "my-sandbox", "echo hello && echo world")
			},
			wantArgs: []string{"ssh", "my-sandbox", "--", "echo hello && echo world"},
		},
		{
			name: "Cp",
			call: func(r *Runner, ctx context.Context) error {
				return r.Cp(ctx, "my-sandbox", "local.txt", ":/remote/path")
			},
			wantArgs: []string{"cp", "my-sandbox", "local.txt", ":/remote/path"},
		},
		{
			name: "Stop",
			call: func(r *Runner, ctx context.Context) error {
				return r.Stop(ctx, "my-sandbox")
			},
			wantArgs: []string{"stop", "my-sandbox"},
		},
		{
			name: "SSHProxy with proxy only",
			call: func(r *Runner, ctx context.Context) error {
				return r.SSHProxy(ctx, "my-sandbox", &SSHOpts{Proxy: true}, "tmux new-session -d -s claude /tmp/run.sh")
			},
			wantArgs: []string{"ssh", "--proxy", "my-sandbox", "--", "tmux new-session -d -s claude /tmp/run.sh"},
		},
		{
			name: "SSHProxy nil opts",
			call: func(r *Runner, ctx context.Context) error {
				return r.SSHProxy(ctx, "my-sandbox", nil, "echo hello")
			},
			wantArgs: []string{"ssh", "my-sandbox", "--", "echo hello"},
		},
		{
			name: "SSHProxy with proxy and reverse tunnels",
			call: func(r *Runner, ctx context.Context) error {
				return r.SSHProxy(ctx, "my-sandbox", &SSHOpts{
					Proxy:          true,
					ReverseTunnels: []string{"19090:localhost:12345"},
				}, "run.sh")
			},
			wantArgs: []string{"ssh", "--proxy", "-R", "19090:localhost:12345", "my-sandbox", "--", "run.sh"},
		},
		{
			name: "SSHProxy reverse tunnel only no proxy",
			call: func(r *Runner, ctx context.Context) error {
				return r.SSHProxy(ctx, "my-sandbox", &SSHOpts{
					ReverseTunnels: []string{"19090:localhost:12345"},
				}, "run.sh")
			},
			wantArgs: []string{"ssh", "-R", "19090:localhost:12345", "my-sandbox", "--", "run.sh"},
		},
		{
			name: "SSHProxyOutput with proxy only",
			call: func(r *Runner, ctx context.Context) error {
				return r.SSHProxyOutput(ctx, "my-sandbox", io.Discard, &SSHOpts{Proxy: true}, "tail -f ~/transcript.jsonl")
			},
			wantArgs: []string{"ssh", "--proxy", "my-sandbox", "--", "tail -f ~/transcript.jsonl"},
		},
		{
			name: "SSHProxyOutput with proxy and reverse tunnels",
			call: func(r *Runner, ctx context.Context) error {
				return r.SSHProxyOutput(ctx, "my-sandbox", io.Discard, &SSHOpts{
					Proxy:          true,
					ReverseTunnels: []string{"19090:localhost:12345", "8080:localhost:3000"},
				}, "run.sh")
			},
			wantArgs: []string{"ssh", "--proxy", "-R", "19090:localhost:12345", "-R", "8080:localhost:3000", "my-sandbox", "--", "run.sh"},
		},
		{
			name: "Down",
			call: func(r *Runner, ctx context.Context) error {
				return r.Down(ctx, "my-sandbox")
			},
			wantArgs: []string{"down", "my-sandbox"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, outFile := writeArgCapture(t)
			r := New(script)
			ctx := context.Background()

			if err := tt.call(r, ctx); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			gotArgs := readArgs(t, outFile)
			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("got %d args %v, want %d args %v", len(gotArgs), gotArgs, len(tt.wantArgs), tt.wantArgs)
			}
			for i, want := range tt.wantArgs {
				if gotArgs[i] != want {
					t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], want)
				}
			}
		})
	}
}
