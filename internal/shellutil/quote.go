package shellutil

import "strings"

// Quote returns s wrapped in single quotes with internal single quotes
// escaped using the '\'' idiom, making it safe to embed in a shell command.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
