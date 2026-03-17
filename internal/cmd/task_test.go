package cmd

import (
	"strings"
	"testing"
)

func TestBuildRunScript(t *testing.T) {
	tests := []struct {
		name            string
		taskDescription string
		continueSession bool
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "new session",
			taskDescription: "Fix the bug in handler.go",
			continueSession: false,
			wantContains: []string{
				"#!/bin/bash",
				"source ~/.bashrc",
				"--dangerously-skip-permissions",
				"--output-format stream-json",
				"--append-system-prompt-file ~/system-prompt.md",
				"'Fix the bug in handler.go'",
				"tee  ~/transcript.jsonl",
			},
			wantNotContains: []string{
				"--continue",
				"tee -a",
				"cd ~/project",
			},
		},
		{
			name:            "continue session",
			taskDescription: "Also fix the tests",
			continueSession: true,
			wantContains: []string{
				"--continue",
				"--append-system-prompt-file ~/system-prompt.md",
				"'Also fix the tests'",
				"tee -a ~/transcript.jsonl",
			},
		},
		{
			name:            "description with single quotes",
			taskDescription: "Fix the 'bug' in handler.go",
			continueSession: false,
			wantContains: []string{
				`'Fix the '\''bug'\'' in handler.go'`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRunScript(tt.taskDescription, tt.continueSession)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("buildRunScript() missing %q\ngot:\n%s", want, got)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(got, notWant) {
					t.Errorf("buildRunScript() should not contain %q\ngot:\n%s", notWant, got)
				}
			}
		})
	}
}
