package sandbox

import (
	"testing"
)

func TestCollapseBashC(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "bash -c with single command",
			input: []string{"bash", "-c", "git config --global user.name Drellabot"},
			want:  []string{"git config --global user.name Drellabot"},
		},
		{
			name:  "bash -c with AsUser wrapper",
			input: []string{"bash", "-c", "su - claude -c 'git config --global user.name Drellabot'"},
			want:  []string{"su - claude -c 'git config --global user.name Drellabot'"},
		},
		{
			name:  "single command without bash -c",
			input: []string{"sync"},
			want:  []string{"sync"},
		},
		{
			name:  "multiple args without bash -c",
			input: []string{"chmod", "+x", "/tmp/script.sh"},
			want:  []string{"chmod", "+x", "/tmp/script.sh"},
		},
		{
			name:  "empty command",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "just bash without -c",
			input: []string{"bash", "script.sh"},
			want:  []string{"bash", "script.sh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseBashC(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("collapseBashC(%v) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("collapseBashC(%v)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
