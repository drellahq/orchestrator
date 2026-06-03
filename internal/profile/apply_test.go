package profile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellahq/orchestrator/internal/agent"
	"github.com/drellahq/orchestrator/internal/sandbox"
	"github.com/drellahq/orchestrator/internal/shellutil"
)

// writeGjollCapture creates a shell script that appends all its arguments to a file,
// one invocation per line (args joined by tabs). Returns the script path and the output path.
func writeGjollCapture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "gjoll")
	outFile := filepath.Join(dir, "args.txt")

	// Write each invocation's args tab-separated on one line
	content := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + outFile + "\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script, outFile
}

func TestRegisterMCPServer_ShellInjection(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	ccBackend, _ := agent.New("claude-code")

	tests := []struct {
		name   string
		server MCPServer
	}{
		{
			name: "shell metacharacters in server name",
			server: MCPServer{
				Name:      `evil; touch /tmp/pwned`,
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"-y", "tool@latest"},
			},
		},
		{
			name: "shell metacharacters in command",
			server: MCPServer{
				Name:      "my-tool",
				Transport: "stdio",
				Command:   `npx; touch /tmp/pwned`,
				Args:      []string{"-y"},
			},
		},
		{
			name: "shell metacharacters in http URL",
			server: MCPServer{
				Name:      "web-tool",
				Transport: "http",
				URL:       `http://localhost:8080/mcp"; touch /tmp/pwned; echo "`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, outFile := writeGjollCapture(t)
			runner := sandbox.NewGjollAdapter(script)

			err := registerMCPServer(context.Background(), runner, ccBackend, "test-sandbox", tt.server)
			if err != nil {
				t.Fatalf("registerMCPServer error: %v", err)
			}

			data, err := os.ReadFile(outFile)
			if err != nil {
				t.Fatalf("reading captured args: %v", err)
			}
			captured := strings.TrimSpace(string(data))

			// The captured output should not contain unquoted dangerous characters
			// that could cause shell injection
			if strings.Contains(captured, "; touch") && !strings.Contains(captured, "'") {
				t.Errorf("potential shell injection in captured args: %q", captured)
			}
		})
	}
}

func TestRunSetup_ShellInjection(t *testing.T) {
	tests := []struct {
		name    string
		sandbox string
	}{
		{
			name:    "semicolon injection",
			sandbox: `test; touch /tmp/pwned`,
		},
		{
			name:    "backtick injection",
			sandbox: "test`touch /tmp/pwned`box",
		},
		{
			name:    "dollar substitution",
			sandbox: `test$(touch /tmp/pwned)box`,
		},
		{
			name:    "spaces in name",
			sandbox: `my sandbox name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupDir := t.TempDir()
			setupPath := filepath.Join(setupDir, "setup.sh")
			if err := os.WriteFile(setupPath, []byte("#!/bin/bash\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}

			sentinel := filepath.Join(t.TempDir(), "pwned")
			gjollScript := filepath.Join(setupDir, "gjoll")
			gjollContent := "#!/bin/bash\nexit 0\n"
			if err := os.WriteFile(gjollScript, []byte(gjollContent), 0755); err != nil {
				t.Fatal(err)
			}

			runner := sandbox.NewGjollAdapter(gjollScript)
			taskDir := t.TempDir()

			outputFile := filepath.Join(t.TempDir(), "helpers_output")
			setupContent := "#!/bin/bash\ncat $(which sandbox-cp) > " + outputFile + "\n"
			if err := os.WriteFile(setupPath, []byte(setupContent), 0755); err != nil {
				t.Fatal(err)
			}

			err := runSetup(context.Background(), runner, tt.sandbox, setupPath, taskDir, nil)
			if err != nil {
				t.Fatalf("runSetup error: %v", err)
			}

			scriptContent, err := os.ReadFile(outputFile)
			if err != nil {
				t.Fatalf("reading generated script: %v", err)
			}

			content := string(scriptContent)

			quoted := shellutil.Quote(tt.sandbox)
			if !strings.Contains(content, quoted) {
				t.Errorf("expected shell-quoted sandbox name %s in script, got:\n%s", quoted, content)
			}

			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("shell injection: sentinel file was created")
			}
		})
	}
}
