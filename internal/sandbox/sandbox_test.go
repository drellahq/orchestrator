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
			runner := NewFromConfig(tt.backend, "fedora:43", "~/.anthropic/api_key", 19090)
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
			r := NewPodman(tt.image, "~/.anthropic/api_key", 19090)
			if r.image != tt.wantImage {
				t.Errorf("image = %q, want %q", r.image, tt.wantImage)
			}
		})
	}
}

func TestTranslatePath(t *testing.T) {
	r := NewPodman("fedora:43", "", 19090)

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
