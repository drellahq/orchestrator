package profile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellabot/orchestrator/internal/gjoll"
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

			// Each MCP server field containing special characters must be
			// individually shell-quoted (single-quoted) in the command
			// string passed after "--".
			//
			// Find the command part after "--"
			parts := strings.SplitN(captured, "-- ", 2)
			if len(parts) < 2 {
				t.Fatalf("expected -- separator in captured args: %q", captured)
			}
			cmdPart := parts[1]

			// Each field with shell metacharacters must be wrapped in
			// single quotes via shellutil.Quote. Verify that dangerous
			// patterns only appear inside single-quoted regions.
			for _, dangerous := range []string{"; ", "$(", "`"} {
				if strings.Contains(cmdPart, dangerous) {
					idx := strings.Index(cmdPart, dangerous)
					prefix := cmdPart[:idx]
					// Odd single-quote count means we're inside a quoted string (OK).
					// Even count means we're outside quotes (BUG).
					if strings.Count(prefix, "'")%2 == 0 {
						t.Errorf("shell metacharacter %q appears unquoted in command: %q", dangerous, cmdPart)
					}
				}
			}
		})
	}
}

func TestRunSetup_ShellInjection(t *testing.T) {
	// Verify that sandbox names with shell metacharacters are properly
	// single-quoted (via shellutil.Quote) in the generated helper scripts.
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

			runner := gjoll.New(gjollScript)
			taskDir := t.TempDir()

			// runSetup generates helper scripts that embed the sandbox name.
			// We intercept them via setup.sh to verify the sandbox name
			// is properly single-quoted via shellutil.Quote.
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

			// The sandbox name must appear properly single-quoted via
			// shellutil.Quote in the generated helper script.
			quoted := shellutil.Quote(tt.sandbox)
			expected := "gjoll cp " + quoted + " "
			if !strings.Contains(content, expected) {
				t.Errorf("expected sandbox to be quoted as %q in script, got:\n%s", expected, content)
			}

			// Also verify sentinel was not created
			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("shell injection: sentinel file was created")
			}
		})
	}
}
