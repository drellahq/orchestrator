package sandbox

import (
	"testing"
)

func TestNewFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		wantType string
	}{
		{
			name:     "podman backend",
			backend:  "podman",
			wantType: "*sandbox.PodmanRunner",
		},
		{
			name:     "gjoll backend",
			backend:  "gjoll",
			wantType: "*sandbox.GjollAdapter",
		},
		{
			name:     "empty defaults to gjoll",
			backend:  "",
			wantType: "*sandbox.GjollAdapter",
		},
		{
			name:     "unknown defaults to gjoll",
			backend:  "docker",
			wantType: "*sandbox.GjollAdapter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewFromConfig(tt.backend, "fedora:43", "~/.anthropic/api_key")
			got := typeString(runner)
			if got != tt.wantType {
				t.Errorf("NewFromConfig(%q) returned %s, want %s", tt.backend, got, tt.wantType)
			}
		})
	}
}

func TestNewPodman(t *testing.T) {
	tests := []struct {
		name      string
		image     string
		wantImage string
	}{
		{
			name:      "default image",
			image:     "",
			wantImage: "fedora:43",
		},
		{
			name:      "custom image",
			image:     "ubuntu:24.04",
			wantImage: "ubuntu:24.04",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewPodman(tt.image, "/abs/path/key")
			if r.image != tt.wantImage {
				t.Errorf("image = %q, want %q", r.image, tt.wantImage)
			}
		})
	}
}

func TestNewPodmanTildeExpansion(t *testing.T) {
	r := NewPodman("", "~/.anthropic/api_key")
	// After construction, tilde should be expanded (if home dir is available)
	if r.anthropicKey == "~/.anthropic/api_key" {
		// This would mean UserHomeDir failed — skip on systems where it might
		t.Skip("UserHomeDir not available, tilde not expanded")
	}
	if r.anthropicKey == "" {
		t.Error("anthropicKey should not be empty after tilde expansion")
	}
	// Verify it's an absolute path now
	if r.anthropicKey[0] != '/' {
		t.Errorf("expected absolute path after tilde expansion, got %q", r.anthropicKey)
	}

	// Absolute paths should pass through unchanged
	r2 := NewPodman("", "/abs/path/key")
	if r2.anthropicKey != "/abs/path/key" {
		t.Errorf("absolute path should be unchanged, got %q", r2.anthropicKey)
	}
}

func TestTranslatePath(t *testing.T) {
	r := NewPodman("fedora:43", "")

	tests := []struct {
		name     string
		contName string
		path     string
		want     string
	}{
		{
			name:     "local path unchanged",
			contName: "test-sandbox",
			path:     "/tmp/local/file.txt",
			want:     "/tmp/local/file.txt",
		},
		{
			name:     "absolute remote path",
			contName: "test-sandbox",
			path:     ":/home/claude/file.txt",
			want:     "test-sandbox:/home/claude/file.txt",
		},
		{
			name:     "tilde home-relative path",
			contName: "test-sandbox",
			path:     ":~/file.txt",
			want:     "test-sandbox:/home/claude/file.txt",
		},
		{
			name:     "tilde home-relative nested path",
			contName: "test-sandbox",
			path:     ":~/.claude/settings.json",
			want:     "test-sandbox:/home/claude/.claude/settings.json",
		},
		{
			name:     "bare tilde",
			contName: "test-sandbox",
			path:     ":~",
			want:     "test-sandbox:/home/claude",
		},
		{
			name:     "colon with absolute path",
			contName: "my-task",
			path:     ":/tmp/run.sh",
			want:     "my-task:/tmp/run.sh",
		},
		{
			name:     "relative local path unchanged",
			contName: "test-sandbox",
			path:     "relative/path.txt",
			want:     "relative/path.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.translatePath(tt.contName, tt.path)
			if got != tt.want {
				t.Errorf("translatePath(%q, %q) = %q, want %q", tt.contName, tt.path, got, tt.want)
			}
		})
	}
}

func TestShellQuoteForSu(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no special chars", "hello", "'hello'"},
		{"with single quote", "it's", "'it'\\''s'"},
		{"multiple single quotes", "'hello' 'world'", "''\\''hello'\\'' '\\''world'\\'''"},
		{"empty string", "", "''"},
		{"double quotes pass through", `"hello"`, `'"hello"'`},
		{"spaces preserved", "hello world", "'hello world'"},
		{"url unchanged", "http://localhost:19090/mcp", "'http://localhost:19090/mcp'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuoteForSu(tt.input)
			if got != tt.want {
				t.Errorf("shellQuoteForSu(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWrapUserCommand(t *testing.T) {
	r := NewPodman("fedora:43", "")

	tests := []struct {
		name    string
		command []string
		want    []string
	}{
		{
			name:    "zero args",
			command: []string{},
			want:    []string{"bash", "-c", "su - claude"},
		},
		{
			name:    "single arg",
			command: []string{"whoami"},
			want:    []string{"bash", "-c", "su - claude -c ''\\''whoami'\\'''"},
		},
		{
			name:    "multiple args",
			command: []string{"git", "config", "--global", "user.name", "Drellabot"},
			want:    []string{"bash", "-c", "su - claude -c ''\\''git'\\'' '\\''config'\\'' '\\''--global'\\'' '\\''user.name'\\'' '\\''Drellabot'\\'''"},
		},
		{
			name:    "args with spaces",
			command: []string{"git", "config", "--global", "user.name", "Jane Doe"},
			want:    []string{"bash", "-c", "su - claude -c ''\\''git'\\'' '\\''config'\\'' '\\''--global'\\'' '\\''user.name'\\'' '\\''Jane Doe'\\'''"},
		},
		{
			name: "args with single quote",
			command: []string{"echo", "it's"},
			// Inner: 'echo' 'it'\''s'
			// Outer shellQuoteForSu wraps the whole inner string
			want: func() []string {
				inner := shellQuoteForSu("echo") + " " + shellQuoteForSu("it's")
				return []string{"bash", "-c", "su - claude -c " + shellQuoteForSu(inner)}
			}(),
		},
		{
			name:    "tilde expanded in args",
			command: []string{"chmod", "+x", "~/run-claude.sh"},
			// ~ must be expanded to /home/claude before quoting, because
			// the inner command runs inside single quotes where bash
			// would not expand ~ on its own.
			want: func() []string {
				inner := shellQuoteForSu("chmod") + " " + shellQuoteForSu("+x") + " " + shellQuoteForSu("/home/claude/run-claude.sh")
				return []string{"bash", "-c", "su - claude -c " + shellQuoteForSu(inner)}
			}(),
		},
		{
			name:    "bare tilde expanded",
			command: []string{"ls", "~"},
			want: func() []string {
				inner := shellQuoteForSu("ls") + " " + shellQuoteForSu("/home/claude")
				return []string{"bash", "-c", "su - claude -c " + shellQuoteForSu(inner)}
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.wrapUserCommand(tt.command...)
			if len(got) != len(tt.want) {
				t.Fatalf("wrapUserCommand(%v) = %v (len %d), want %v (len %d)", tt.command, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("wrapUserCommand(%v)[%d] = %q, want %q", tt.command, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExpandTilde(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"home-relative", "~/file.txt", "/home/claude/file.txt"},
		{"bare tilde", "~", "/home/claude"},
		{"absolute path unchanged", "/tmp/file.txt", "/tmp/file.txt"},
		{"nested tilde path", "~/.claude/settings.json", "/home/claude/.claude/settings.json"},
		{"empty string", "", ""},
		{"tilde in middle unchanged", "foo/~/bar", "foo/~/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandTilde(tt.input)
			if got != tt.want {
				t.Errorf("expandTilde(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// typeString returns the type of a Runner as a string for test comparison.
func typeString(r Runner) string {
	switch r.(type) {
	case *PodmanRunner:
		return "*sandbox.PodmanRunner"
	case *GjollAdapter:
		return "*sandbox.GjollAdapter"
	default:
		return "unknown"
	}
}
