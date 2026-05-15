package profile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellabot/orchestrator/internal/gjoll"
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
			runner := gjoll.New(script)

			err := registerMCPServer(context.Background(), runner, "test-sandbox", tt.server)
			if err != nil {
				t.Fatalf("registerMCPServer error: %v", err)
			}

			// Read what was passed to the fake gjoll binary.
			// The command arrives as: ssh test-sandbox -- <joined-command>
			// The joined command should have properly quoted/escaped fields
			// so that the remote shell treats each field literally.
			data, err := os.ReadFile(outFile)
			if err != nil {
				t.Fatalf("reading captured args: %v", err)
			}
			captured := strings.TrimSpace(string(data))

			// The captured command line must not contain unquoted shell metacharacters.
			// Specifically, each MCP server field that contains special characters
			// should be individually shell-quoted in the command string passed after "--".
			//
			// Find the command part after "--"
			parts := strings.SplitN(captured, "-- ", 2)
			if len(parts) < 2 {
				t.Fatalf("expected -- separator in captured args: %q", captured)
			}
			cmdPart := parts[1]

			// The command should properly quote fields with shell metacharacters.
			// Currently, the code joins args with spaces and no quoting, so
			// metacharacters like ; $ ` will be interpreted by the remote shell.
			// After the fix, each field should be shell-quoted.
			for _, dangerous := range []string{"; ", "$(", "`"} {
				// Check if any dangerous unquoted pattern appears in the command.
				// After proper quoting, these should be inside single quotes.
				if strings.Contains(cmdPart, dangerous) {
					// Verify it's inside quotes. Simple heuristic: the dangerous
					// pattern should be preceded by a single-quote on the same line.
					// A proper fix would shell-quote each argument.
					idx := strings.Index(cmdPart, dangerous)
					prefix := cmdPart[:idx]
					// Count single quotes - if odd, we're inside a quoted string (OK)
					// If even, we're outside quotes (BUG)
					if strings.Count(prefix, "'")%2 == 0 {
						t.Errorf("shell metacharacter %q appears unquoted in command: %q", dangerous, cmdPart)
					}
				}
			}
		})
	}
}

func TestRunSetup_ShellInjection(t *testing.T) {
	// Test that sandbox names with shell metacharacters are properly quoted
	// in the generated helper scripts.
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

			// Create a fake gjoll that captures what was called
			sentinel := filepath.Join(t.TempDir(), "pwned")
			gjollScript := filepath.Join(setupDir, "gjoll")
			// The fake gjoll script just records it was called. If the sandbox
			// name is not quoted, the shell will try to execute the injected
			// commands via the generated helper scripts.
			gjollContent := "#!/bin/bash\nexit 0\n"
			if err := os.WriteFile(gjollScript, []byte(gjollContent), 0755); err != nil {
				t.Fatal(err)
			}

			runner := gjoll.New(gjollScript)
			taskDir := t.TempDir()

			// runSetup generates helper scripts. If sandbox name is not quoted,
			// the generated scripts contain shell injection.
			// We can't easily test the full execution path because runSetup
			// runs bash with the setup script, but we can at least check that
			// the generated helper scripts properly quote the sandbox name.
			//
			// Since runSetup creates temp files and immediately runs setup.sh,
			// we need to intercept the generated scripts. We'll do this by
			// having setup.sh cat the helper scripts and check their contents.
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

			// The sandbox name must be properly quoted in the generated script.
			// Currently it's interpolated via fmt.Sprintf without quoting:
			//   gjoll cp <sandbox> "$1" "$2"
			// If sandbox contains metacharacters, the shell will interpret them.
			// After the fix, sandbox should be quoted, e.g.:
			//   gjoll cp 'test; touch /tmp/pwned' "$1" "$2"

			// Check: the sandbox name should NOT appear as bare unquoted text
			// if it contains shell metacharacters.
			if strings.Contains(tt.sandbox, ";") ||
				strings.Contains(tt.sandbox, "`") ||
				strings.Contains(tt.sandbox, "$") ||
				strings.Contains(tt.sandbox, " ") {
				// The sandbox value should be inside quotes in the script
				// Check it's not just bare: "gjoll cp test; touch /tmp/pwned"
				if strings.Contains(content, "gjoll cp "+tt.sandbox+" ") ||
					strings.Contains(content, "gjoll cp "+tt.sandbox+"\n") {
					t.Errorf("sandbox name appears unquoted in generated script:\n%s", content)
				}
			}

			// Also verify sentinel was not created
			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("shell injection: sentinel file was created")
			}
		})
	}
}
