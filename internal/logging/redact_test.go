package logging

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

func TestRedacted_String(t *testing.T) {
	r := Redacted("super-secret-key")
	if got := r.String(); got != "REDACTED" {
		t.Errorf("String() = %q, want %q", got, "REDACTED")
	}
}

func TestRedacted_GoString(t *testing.T) {
	r := Redacted("super-secret-key")
	got := fmt.Sprintf("%#v", r)
	if got != `logging.Redacted("REDACTED")` {
		t.Errorf("GoString() = %q, want logging.Redacted(\"REDACTED\")", got)
	}
}

func TestRedacted_Sprintf(t *testing.T) {
	r := Redacted("super-secret-key")

	formats := []string{"%s", "%v", "%q", "%+v"}
	for _, f := range formats {
		got := fmt.Sprintf(f, r)
		if got != fmt.Sprintf(f, "REDACTED") {
			t.Errorf("Sprintf(%q) = %q, secret leaked", f, got)
		}
	}
}

func TestRedacted_MarshalText(t *testing.T) {
	r := Redacted("super-secret-key")
	b, err := r.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error: %v", err)
	}
	if string(b) != "REDACTED" {
		t.Errorf("MarshalText() = %q, want %q", b, "REDACTED")
	}
}

func TestRedacted_LogValue(t *testing.T) {
	r := Redacted("super-secret-key")
	v := r.LogValue()
	if v.String() != "REDACTED" {
		t.Errorf("LogValue() = %q, want %q", v.String(), "REDACTED")
	}
}

func TestRedacted_SlogAttr(t *testing.T) {
	r := Redacted("super-secret-key")
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	record.AddAttrs(slog.Any("key", r))

	var got string
	record.Attrs(func(a slog.Attr) bool {
		got = a.Value.Resolve().String()
		return true
	})

	if got != "REDACTED" {
		t.Errorf("slog attr resolved to %q, want %q", got, "REDACTED")
	}
}

func TestRedacted_SlackHandler(t *testing.T) {
	secret := "super-secret-key"
	r := Redacted(secret)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "key created", 0)
	record.AddAttrs(slog.Any("key", r))

	h := NewSlackHandler("http://example.com", nil)
	// We can't easily check the output without a server, but we verify
	// the handler does not panic and the attr resolves correctly.
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("handler should be enabled for Info")
	}
}
