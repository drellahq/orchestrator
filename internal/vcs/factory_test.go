package vcs_test

import (
	"testing"

	// Import github for side-effect registration via init().
	_ "github.com/drellabot/orchestrator/internal/github"

	"github.com/drellabot/orchestrator/internal/vcs"
)

func TestNewProvider(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"github explicit", "github", false},
		{"empty defaults to github", "", false},
		{"unsupported", "gitlab", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := vcs.NewProvider(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Error("expected non-nil provider")
			}
		})
	}
}
