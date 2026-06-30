package cmd

import (
	"testing"
)

func TestEmptyDash(t *testing.T) {
	if got := emptyDash(""); got != "-" {
		t.Errorf("emptyDash(\"\") = %q, want %q", got, "-")
	}
	if got := emptyDash("waiting"); got != "waiting" {
		t.Errorf("emptyDash(\"waiting\") = %q, want waiting", got)
	}
}
