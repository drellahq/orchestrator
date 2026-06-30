package sandbox

import (
	"strings"
	"testing"
)

func TestRHELRegistration_RegisterScript(t *testing.T) {
	reg := &RHELRegistration{
		OrgID:         "1234567",
		ActivationKey: "orchestrator-test-key",
	}
	script := reg.RegisterScript()

	for _, want := range []string{
		"sudo dnf install -y subscription-manager",
		"sudo subscription-manager register",
		"'1234567'",
		"'orchestrator-test-key'",
		"sudo subscription-manager status",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("RegisterScript() missing %q\nscript:\n%s", want, script)
		}
	}
}

func TestRHELRegistration_RegisterScript_escapesQuotes(t *testing.T) {
	reg := &RHELRegistration{
		OrgID:         "org'123",
		ActivationKey: "key'456",
	}
	script := reg.RegisterScript()
	if !strings.Contains(script, `'org'\''123'`) {
		t.Errorf("org ID not shell-escaped in script:\n%s", script)
	}
	if !strings.Contains(script, `'key'\''456'`) {
		t.Errorf("activation key not shell-escaped in script:\n%s", script)
	}
}

func TestNewFromConfig_podmanWithRHELKeepsConfiguredImage(t *testing.T) {
	runner, err := NewFromConfig("podman", "", "fedora:43", "", 0, "", &RHELRegistration{
		OrgID:         "123",
		ActivationKey: "ak",
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	p, ok := runner.(*PodmanRunner)
	if !ok {
		t.Fatalf("expected *PodmanRunner, got %T", runner)
	}
	if p.image != "fedora:43" {
		t.Errorf("image = %q, want fedora:43", p.image)
	}
	if p.rhel == nil || p.rhel.OrgID != "123" {
		t.Errorf("rhel registration not wired: %+v", p.rhel)
	}
}

func TestNewFromConfig_gjollWithRHELWiresRegistration(t *testing.T) {
	runner, err := NewFromConfig("gjoll", "", "", "", 0, "", &RHELRegistration{
		OrgID:         "123",
		ActivationKey: "ak",
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	g, ok := runner.(*GjollAdapter)
	if !ok {
		t.Fatalf("expected *GjollAdapter, got %T", runner)
	}
	if g.rhel == nil || g.rhel.OrgID != "123" {
		t.Errorf("rhel registration not wired: %+v", g.rhel)
	}
}

func TestNewFromConfig_podmanWithoutRHELUsesDefaultImage(t *testing.T) {
	runner, err := NewFromConfig("podman", "", "fedora:43", "", 0, "", nil)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	p := runner.(*PodmanRunner)
	if p.image != "fedora:43" {
		t.Errorf("image = %q, want fedora:43", p.image)
	}
	if p.rhel != nil {
		t.Errorf("expected no RHEL registration, got %+v", p.rhel)
	}
}
