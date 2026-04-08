package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MinimalProfile(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "test-profile")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "CLAUDE.md"), []byte("# Test Profile\n\nDo the thing."), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := Load(dir, "test-profile")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if p.Name != "test-profile" {
		t.Errorf("Name = %q, want %q", p.Name, "test-profile")
	}
	if p.Claudemd != "# Test Profile\n\nDo the thing." {
		t.Errorf("Claudemd = %q, want %q", p.Claudemd, "# Test Profile\n\nDo the thing.")
	}
	if p.Setup != "" {
		t.Errorf("Setup = %q, want empty", p.Setup)
	}
	if p.MCP != nil {
		t.Errorf("MCP = %v, want nil", p.MCP)
	}
	if p.Settings != "" {
		t.Errorf("Settings = %q, want empty", p.Settings)
	}
}

func TestLoad_FullProfile(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "full")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(profileDir, "CLAUDE.md"), []byte("instructions"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "setup.sh"), []byte("#!/bin/bash\necho hi"), 0755); err != nil {
		t.Fatal(err)
	}
	mcpYAML := `servers:
  - name: my-tool
    transport: stdio
    command: npx
    args: ["-y", "my-tool@latest"]
  - name: web-tool
    transport: http
    url: http://localhost:8080/mcp
    scope: user
`
	if err := os.WriteFile(filepath.Join(profileDir, "mcp.yaml"), []byte(mcpYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "settings.json"), []byte(`{"key":"value"}`), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := Load(dir, "full")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if p.Claudemd != "instructions" {
		t.Errorf("Claudemd = %q", p.Claudemd)
	}
	if p.Setup == "" {
		t.Error("Setup should be set")
	}
	if p.Settings == "" {
		t.Error("Settings should be set")
	}
	if p.MCP == nil {
		t.Fatal("MCP should not be nil")
	}
	if len(p.MCP.Servers) != 2 {
		t.Fatalf("MCP.Servers = %d, want 2", len(p.MCP.Servers))
	}

	s0 := p.MCP.Servers[0]
	if s0.Name != "my-tool" || s0.Transport != "stdio" || s0.Command != "npx" {
		t.Errorf("Server[0] = %+v", s0)
	}
	if len(s0.Args) != 2 || s0.Args[0] != "-y" {
		t.Errorf("Server[0].Args = %v", s0.Args)
	}

	s1 := p.MCP.Servers[1]
	if s1.Name != "web-tool" || s1.Transport != "http" || s1.URL != "http://localhost:8080/mcp" || s1.Scope != "user" {
		t.Errorf("Server[1] = %+v", s1)
	}
}

func TestLoad_MissingCLAUDEmd(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "no-claude")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir, "no-claude")
	if err == nil {
		t.Fatal("expected error for missing CLAUDE.md")
	}
}

func TestLoad_ProfileNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
}

func TestLoad_InvalidMCPYaml(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "bad-mcp")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "CLAUDE.md"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "mcp.yaml"), []byte("{{invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir, "bad-mcp")
	if err == nil {
		t.Fatal("expected error for invalid mcp.yaml")
	}
}

func TestValidateMCP_MissingName(t *testing.T) {
	cfg := &MCPConfig{
		Servers: []MCPServer{{Transport: "stdio", Command: "test"}},
	}
	if err := validateMCP(cfg); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidateMCP_StdioMissingCommand(t *testing.T) {
	cfg := &MCPConfig{
		Servers: []MCPServer{{Name: "test", Transport: "stdio"}},
	}
	if err := validateMCP(cfg); err == nil {
		t.Fatal("expected error for stdio without command")
	}
}

func TestValidateMCP_HttpMissingURL(t *testing.T) {
	cfg := &MCPConfig{
		Servers: []MCPServer{{Name: "test", Transport: "http"}},
	}
	if err := validateMCP(cfg); err == nil {
		t.Fatal("expected error for http without url")
	}
}

func TestValidateMCP_UnsupportedTransport(t *testing.T) {
	cfg := &MCPConfig{
		Servers: []MCPServer{{Name: "test", Transport: "grpc"}},
	}
	if err := validateMCP(cfg); err == nil {
		t.Fatal("expected error for unsupported transport")
	}
}

func TestValidateMCP_Valid(t *testing.T) {
	cfg := &MCPConfig{
		Servers: []MCPServer{
			{Name: "a", Transport: "stdio", Command: "cmd"},
			{Name: "b", Transport: "http", URL: "http://localhost:8080"},
		},
	}
	if err := validateMCP(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	// Create a file where the profile should be a directory
	if err := os.WriteFile(filepath.Join(dir, "not-a-dir"), []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir, "not-a-dir")
	if err == nil {
		t.Fatal("expected error for non-directory profile")
	}
}
