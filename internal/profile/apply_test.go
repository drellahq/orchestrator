package profile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellabot/orchestrator/internal/sandbox"
	"github.com/drellabot/orchestrator/internal/shellutil"
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
			name: "shell metacharacters in args",
			server: MCPServer{
				Name:      "my-tool",
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{`-y"; touch /tmp/pwned; echo "`, "tool@latest"},
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
		{
			name: "backtick command substitution in name",
			server: MCPServer{
				Name:      "evil`touch /tmp/pwned`tool",
				Transport: "stdio",
				Command:   "npx",
			},
		},
		{
			name: "dollar command substitution in args",
			server: MCPServer{
				Name:      "my-tool",
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"$(touch /tmp/pwned)"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, outFile := writeGjollCapture(t)
			runner := sandbox.NewGjollAdapter(script)

			err := registerMCPServer(context.Background(), runner, "test-sandbox", tt.server)
			if err != nil {
				t.Fatalf("registerMCPServer error: %v", err)
			}

			// Read what was passed to the fake gjoll binary.
			data, err := os.ReadFile(outFile)
			if err != nil {
				t.Fatalf("reading captured args: %v", err)
			}
			captured := strings.TrimSpace(string(data))

			// Find the command part after "--"
			parts := strings.SplitN(captured, "-- ", 2)
			if len(parts) < 2 {
				t.Fatalf("expected -- separator in captured args: %q", captured)
			}
			cmdPart := parts[1]

			// Verify that each user-supplied field with dangerous characters
			// is wrapped in single quotes (shellutil.Quote format).
			// shellutil.Quote wraps in '...' and escapes internal ' as '\''.
			// We check that each dangerous field value appears inside a
			// quoted region by looking for 'value' (possibly with '\'' escapes).
			dangerousFields := []string{tt.server.Name, tt.server.Command, tt.server.URL}
			dangerousFields = append(dangerousFields, tt.server.Args...)
			for _, field := range dangerousFields {
				if field == "" {
					continue
				}
				hasMeta := false
				for _, meta := range []string{";", "$(", "`", "\"", " "} {
					if strings.Contains(field, meta) {
						hasMeta = true
						break
					}
				}
				if !hasMeta {
					continue
				}
				// The field should appear quoted. shellutil.Quote produces
				// 'field' with internal ' replaced by '\''.
				// Check that the field does NOT appear as a bare unquoted token.
				// A properly quoted field will be surrounded by single quotes
				// in the captured command.
				quoted := "'" + strings.ReplaceAll(field, "'", `'\''`) + "'"
				if !strings.Contains(cmdPart, quoted) {
					t.Errorf("field %q not properly shell-quoted in command: %q", field, cmdPart)
				}
			}
		})
	}
}

func TestRunSetup_ShellInjection(t *testing.T) {
	// Verify that sandbox names with shell metacharacters are properly
	// embedded in the generated helper scripts.
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
			// Create a minimal setup.sh that just exits 0
			setupDir := t.TempDir()
			setupPath := filepath.Join(setupDir, "setup.sh")
			if err := os.WriteFile(setupPath, []byte("#!/bin/bash\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}

			// Create a fake gjoll that does nothing
			sentinel := filepath.Join(t.TempDir(), "pwned")
			gjollScript := filepath.Join(setupDir, "gjoll")
			gjollContent := "#!/bin/bash\nexit 0\n"
			if err := os.WriteFile(gjollScript, []byte(gjollContent), 0755); err != nil {
				t.Fatal(err)
			}

			runner := sandbox.NewGjollAdapter(gjollScript)
			taskDir := t.TempDir()

			// runSetup generates helper scripts that embed the sandbox name.
			// We intercept them via setup.sh to verify the sandbox name
			// appears in the generated helper script.
			outputFile := filepath.Join(t.TempDir(), "helpers_output")
			setupContent := "#!/bin/bash\ncat $(which sandbox-cp) > " + outputFile + "\n"
			if err := os.WriteFile(setupPath, []byte(setupContent), 0755); err != nil {
				t.Fatal(err)
			}

			err := runSetup(context.Background(), runner, tt.sandbox, setupPath, taskDir, nil)
			if err != nil {
				t.Fatalf("runSetup error: %v", err)
			}

			// Read the generated sandbox-cp script
			scriptContent, err := os.ReadFile(outputFile)
			if err != nil {
				t.Fatalf("reading generated script: %v", err)
			}

			content := string(scriptContent)

			// The sandbox name must be shell-quoted in the generated helper script.
			quoted := shellutil.Quote(tt.sandbox)
			if !strings.Contains(content, quoted) {
				t.Errorf("expected shell-quoted sandbox name %s in script, got:\n%s", quoted, content)
			}

			// Also verify sentinel was not created
			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("shell injection: sentinel file was created")
			}
		})
	}
}
