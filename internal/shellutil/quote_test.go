package shellutil

import "testing"

func TestQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple string", input: "hello", want: "'hello'"},
		{name: "empty string", input: "", want: "''"},
		{name: "single quote", input: "it's", want: `'it'\''s'`},
		{name: "only single quotes", input: "'''", want: `''\'''\'''\'''`},
		{name: "double quotes", input: `he said "hi"`, want: `'he said "hi"'`},
		{name: "semicolon", input: "a; rm -rf /", want: "'a; rm -rf /'"},
		{name: "dollar substitution", input: "$(evil)", want: "'$(evil)'"},
		{name: "backtick substitution", input: "`evil`", want: "'`evil`'"},
		{name: "newline", input: "line1\nline2", want: "'line1\nline2'"},
		{name: "spaces", input: "hello world", want: "'hello world'"},
		{name: "mixed special chars", input: `it's a "test" $(cmd)`, want: `'it'\''s a "test" $(cmd)'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Quote(tt.input)
			if got != tt.want {
				t.Errorf("Quote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
