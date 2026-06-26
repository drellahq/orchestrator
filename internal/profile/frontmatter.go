package profile

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
)

// FrontMatter holds the parsed front matter from an issue body.
type FrontMatter struct {
	Profile      string
	AgentBackend string
	Vars         map[string]string
	Description  string
}

// ParseFrontMatter extracts YAML front matter from an issue body.
//
// The front matter is delimited by "```" or "---" lines at the start of the body:
//
//	```
//	profile: code-review
//	agent: opencode
//	repo: org/repo
//	pr: 42
//	```
//
//	Review this pull request.
//
// Returns the profile name (from the "profile" key), agent backend (from the
// "agent" key), remaining key-value pairs as PROFILE_* environment variable
// mappings, and the body after the front matter.
// If no front matter is present, profile and vars are empty and the full body is returned.
func ParseFrontMatter(body string) (profile string, vars map[string]string, description string, err error) {
	fm, err := ParseFrontMatterFull(body)
	if err != nil {
		return "", nil, body, err
	}
	return fm.Profile, fm.Vars, fm.Description, nil
}

// ParseFrontMatterFull extracts YAML front matter from an issue body,
// returning all parsed fields including the agent backend.
func ParseFrontMatterFull(body string) (*FrontMatter, error) {
	fm := &FrontMatter{
		Vars:        make(map[string]string),
		Description: body,
	}

	trimmed := strings.TrimLeftFunc(body, unicode.IsSpace)

	var delimiter string
	switch {
	case strings.HasPrefix(trimmed, "```"):
		delimiter = "```"
	case strings.HasPrefix(trimmed, "---"):
		delimiter = "---"
	default:
		return fm, nil
	}

	rest := trimmed[len(delimiter):]
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) == 0 || rest[0] != '\n' {
		return fm, nil
	}
	rest = rest[1:]

	closeMarker := "\n" + delimiter
	closeIdx := strings.Index(rest, closeMarker)
	if closeIdx == -1 {
		return fm, nil
	}

	fmContent := rest[:closeIdx]
	afterClose := rest[closeIdx+len(closeMarker):]

	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(fmContent), &raw); err != nil {
		return nil, err
	}

	if v, ok := raw["profile"]; ok {
		fm.Profile = toString(v)
		delete(raw, "profile")
	}

	if v, ok := raw["agent"]; ok {
		fm.AgentBackend = toString(v)
		delete(raw, "agent")
	}

	for k, v := range raw {
		envKey := "PROFILE_" + toEnvKey(k)
		fm.Vars[envKey] = toString(v)
	}

	fm.Description = strings.TrimLeftFunc(afterClose, unicode.IsSpace)

	return fm, nil
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
