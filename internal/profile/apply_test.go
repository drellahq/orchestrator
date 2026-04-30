package profile

import (
	"testing"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no special chars",
			input: "hello",
			want:  "'hello'",
		},
		{
			name:  "with single quote",
			input: "it's",
			want:  "'it'\\''s'",
		},
		{
			name:  "multiple single quotes",
			input: "'hello' 'world'",
			want:  "''\\''hello'\\'' '\\''world'\\'''",
		},
		{
			name:  "empty string",
			input: "",
			want:  "''",
		},
		{
			name:  "double quotes pass through",
			input: `"hello"`,
			want:  `'"hello"'`,
		},
		{
			name:  "spaces preserved inside quotes",
			input: "hello world",
			want:  "'hello world'",
		},
		{
			name:  "url unchanged",
			input: "http://localhost:19090/mcp",
			want:  "'http://localhost:19090/mcp'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
