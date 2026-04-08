package profile

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
)

// ParseFrontMatter extracts YAML front matter from an issue body.
//
// The front matter is delimited by "---" lines at the start of the body:
//
//	---
//	profile: code-review
//	repo: org/repo
//	pr: 42
//	---
//
//	Review this pull request.
//
// Returns the profile name (from the "profile" key), remaining key-value pairs
// as PROFILE_* environment variable mappings, and the body after the front matter.
// If no front matter is present, profile and vars are empty and the full body is returned.
func ParseFrontMatter(body string) (profile string, vars map[string]string, description string, err error) {
	vars = make(map[string]string)

	trimmed := strings.TrimLeftFunc(body, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, "---") {
		return "", vars, body, nil
	}

	// Find the closing delimiter
	rest := trimmed[3:] // skip opening "---"
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) == 0 || rest[0] != '\n' {
		// "---" followed by non-whitespace content is not front matter
		return "", vars, body, nil
	}
	rest = rest[1:] // skip the newline after "---"

	closeIdx := strings.Index(rest, "\n---")
	if closeIdx == -1 {
		// No closing delimiter — treat as no front matter
		return "", vars, body, nil
	}

	fmContent := rest[:closeIdx]
	afterClose := rest[closeIdx+4:] // skip "\n---"

	// Parse YAML front matter
	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(fmContent), &raw); err != nil {
		return "", vars, body, err
	}

	// Extract profile key
	if v, ok := raw["profile"]; ok {
		profile = toString(v)
		delete(raw, "profile")
	}

	// Convert remaining keys to PROFILE_* env vars
	for k, v := range raw {
		envKey := "PROFILE_" + toEnvKey(k)
		vars[envKey] = toString(v)
	}

	// The description is everything after the closing delimiter,
	// with leading whitespace trimmed.
	description = strings.TrimLeftFunc(afterClose, unicode.IsSpace)

	return profile, vars, description, nil
}

// toEnvKey converts a front matter key to an environment variable suffix:
// uppercase, hyphens to underscores.
func toEnvKey(key string) string {
	s := strings.ToUpper(key)
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strings.TrimRight(strings.TrimRight(
			strings.Replace(fmt.Sprintf("%v", val), " ", "", -1), "0"), ".")
	default:
		return fmt.Sprintf("%v", val)
	}
}
