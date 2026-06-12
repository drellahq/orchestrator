package profile

import (
	"testing"
)

func TestParseFrontMatter_WithProfile(t *testing.T) {
	body := `---
profile: code-review
repo: org/repo
pr: 42
---

Review this pull request.`

	profile, vars, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	if profile != "code-review" {
		t.Errorf("profile = %q, want %q", profile, "code-review")
	}

	if vars["PROFILE_REPO"] != "org/repo" {
		t.Errorf("PROFILE_REPO = %q, want %q", vars["PROFILE_REPO"], "org/repo")
	}
	if vars["PROFILE_PR"] != "42" {
		t.Errorf("PROFILE_PR = %q, want %q", vars["PROFILE_PR"], "42")
	}

	if desc != "Review this pull request." {
		t.Errorf("description = %q, want %q", desc, "Review this pull request.")
	}
}

func TestParseFrontMatter_NoFrontMatter(t *testing.T) {
	body := "Just a regular issue body.\n\nWith paragraphs."

	profile, vars, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	if profile != "" {
		t.Errorf("profile = %q, want empty", profile)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v, want empty", vars)
	}
	if desc != body {
		t.Errorf("description = %q, want original body", desc)
	}
}

func TestParseFrontMatter_NoClosingDelimiter(t *testing.T) {
	body := `---
profile: test
key: value

Body without closing delimiter.`

	profile, vars, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	// No closing delimiter means it's not valid front matter
	if profile != "" {
		t.Errorf("profile = %q, want empty", profile)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v, want empty", vars)
	}
	if desc != body {
		t.Errorf("description should be original body")
	}
}

func TestParseFrontMatter_ProfileOnly(t *testing.T) {
	body := `---
profile: default
---

Do some work.`

	profile, vars, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	if profile != "default" {
		t.Errorf("profile = %q, want %q", profile, "default")
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v, want empty", vars)
	}
	if desc != "Do some work." {
		t.Errorf("description = %q, want %q", desc, "Do some work.")
	}
}

func TestParseFrontMatter_HyphenatedKeys(t *testing.T) {
	body := `---
profile: test
target-branch: main
pr-number: 99
---

Body text.`

	profile, vars, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	if profile != "test" {
		t.Errorf("profile = %q, want %q", profile, "test")
	}
	if vars["PROFILE_TARGET_BRANCH"] != "main" {
		t.Errorf("PROFILE_TARGET_BRANCH = %q, want %q", vars["PROFILE_TARGET_BRANCH"], "main")
	}
	if vars["PROFILE_PR_NUMBER"] != "99" {
		t.Errorf("PROFILE_PR_NUMBER = %q, want %q", vars["PROFILE_PR_NUMBER"], "99")
	}
	if desc != "Body text." {
		t.Errorf("description = %q", desc)
	}
}

func TestParseFrontMatter_EmptyBody(t *testing.T) {
	profile, vars, desc, err := ParseFrontMatter("")
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}
	if profile != "" {
		t.Errorf("profile = %q, want empty", profile)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v, want empty", vars)
	}
	if desc != "" {
		t.Errorf("description = %q, want empty", desc)
	}
}

func TestParseFrontMatter_InvalidYAML(t *testing.T) {
	body := `---
{{invalid yaml
---

Body.`

	_, _, _, err := ParseFrontMatter(body)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseFrontMatter_NoProfileKey(t *testing.T) {
	body := `---
repo: org/repo
pr: 42
---

Body.`

	profile, vars, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	if profile != "" {
		t.Errorf("profile = %q, want empty", profile)
	}
	if vars["PROFILE_REPO"] != "org/repo" {
		t.Errorf("PROFILE_REPO = %q", vars["PROFILE_REPO"])
	}
	if vars["PROFILE_PR"] != "42" {
		t.Errorf("PROFILE_PR = %q", vars["PROFILE_PR"])
	}
	if desc != "Body." {
		t.Errorf("description = %q", desc)
	}
}

func TestParseFrontMatter_LeadingWhitespace(t *testing.T) {
	body := `
---
profile: test
---

Body.`

	profile, _, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	if profile != "test" {
		t.Errorf("profile = %q, want %q", profile, "test")
	}
	if desc != "Body." {
		t.Errorf("description = %q", desc)
	}
}

func TestParseFrontMatter_EmptyDescription(t *testing.T) {
	body := `---
profile: test
---
`

	profile, _, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}

	if profile != "test" {
		t.Errorf("profile = %q, want %q", profile, "test")
	}
	if desc != "" {
		t.Errorf("description = %q, want empty", desc)
	}
}

func TestParseFrontMatter_AgentBackend(t *testing.T) {
	body := `---
profile: code-review
agent: opencode
repo: org/repo
---

Review this PR.`

	fm, err := ParseFrontMatterFull(body)
	if err != nil {
		t.Fatalf("ParseFrontMatterFull() error: %v", err)
	}

	if fm.Profile != "code-review" {
		t.Errorf("Profile = %q, want %q", fm.Profile, "code-review")
	}
	if fm.AgentBackend != "opencode" {
		t.Errorf("AgentBackend = %q, want %q", fm.AgentBackend, "opencode")
	}
	if fm.Vars["PROFILE_REPO"] != "org/repo" {
		t.Errorf("PROFILE_REPO = %q, want %q", fm.Vars["PROFILE_REPO"], "org/repo")
	}
	if fm.Description != "Review this PR." {
		t.Errorf("Description = %q, want %q", fm.Description, "Review this PR.")
	}
	// "agent" should not appear as a PROFILE_* var
	if _, ok := fm.Vars["PROFILE_AGENT"]; ok {
		t.Error("agent key should not appear as PROFILE_AGENT var")
	}
}

func TestParseFrontMatter_AgentOnly(t *testing.T) {
	body := `---
agent: opencode
---

Do some work.`

	fm, err := ParseFrontMatterFull(body)
	if err != nil {
		t.Fatalf("ParseFrontMatterFull() error: %v", err)
	}

	if fm.Profile != "" {
		t.Errorf("Profile = %q, want empty", fm.Profile)
	}
	if fm.AgentBackend != "opencode" {
		t.Errorf("AgentBackend = %q, want %q", fm.AgentBackend, "opencode")
	}
	if fm.Description != "Do some work." {
		t.Errorf("Description = %q, want %q", fm.Description, "Do some work.")
	}
}

func TestParseFrontMatter_BackwardCompatible(t *testing.T) {
	body := `---
profile: code-review
repo: org/repo
pr: 42
---

Review this pull request.`

	// The original ParseFrontMatter should still work unchanged
	profile, vars, desc, err := ParseFrontMatter(body)
	if err != nil {
		t.Fatalf("ParseFrontMatter() error: %v", err)
	}
	if profile != "code-review" {
		t.Errorf("profile = %q, want %q", profile, "code-review")
	}
	if vars["PROFILE_REPO"] != "org/repo" {
		t.Errorf("PROFILE_REPO = %q", vars["PROFILE_REPO"])
	}
	if desc != "Review this pull request." {
		t.Errorf("description = %q", desc)
	}
}

func TestToEnvKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"repo", "REPO"},
		{"pr-number", "PR_NUMBER"},
		{"target-branch", "TARGET_BRANCH"},
		{"ALREADY_UPPER", "ALREADY_UPPER"},
	}
	for _, tt := range tests {
		got := toEnvKey(tt.input)
		if got != tt.want {
			t.Errorf("toEnvKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
