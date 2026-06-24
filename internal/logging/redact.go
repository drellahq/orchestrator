package logging

import "log/slog"

// Redacted wraps a string so it is masked in log output and
// fmt.Stringer contexts. Use it for values that must never appear in
// logs, such as activation keys or tokens.
type Redacted string

func (Redacted) String() string            { return "REDACTED" }
func (Redacted) LogValue() slog.Value      { return slog.StringValue("REDACTED") }
func (Redacted) GoString() string          { return `logging.Redacted("REDACTED")` }
func (Redacted) MarshalText() ([]byte, error) { return []byte("REDACTED"), nil }
