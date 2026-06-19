package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	gh "github.com/drellabot/orchestrator/internal/github"
)

func writeOrgMembersScript(t *testing.T, members map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")

	var cases []string
	for org, logins := range members {
		lines := ""
		for _, l := range strings.Split(logins, ",") {
			lines += `printf '%s\n' "` + l + `"` + "\n"
		}
		cases = append(cases, `*"/orgs/`+org+`/members"*) `+lines+`exit 0 ;;`)
	}

	content := `#!/bin/sh
ARGS="$*"
case "$ARGS" in
` + strings.Join(cases, "\n") + `
*) printf ''; exit 0 ;;
esac
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestResolveCommenters_StaticOnly(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script := writeOrgMembersScript(t, nil)
	runner := gh.New(script)

	rc := ResolveCommenters(context.Background(), runner, []string{"alice", "bob"}, nil)

	want := []string{"alice", "bob"}
	if !reflect.DeepEqual(rc.Merged, want) {
		t.Errorf("Merged = %v, want %v", rc.Merged, want)
	}
	if len(rc.Orgs) != 0 {
		t.Errorf("Orgs = %v, want empty", rc.Orgs)
	}
}

func TestResolveCommenters_WithOrgs(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script := writeOrgMembersScript(t, map[string]string{
		"myorg": "charlie,dave",
	})
	runner := gh.New(script)

	orgs := map[string]string{"myorg": "member"}
	rc := ResolveCommenters(context.Background(), runner, []string{"alice"}, orgs)

	want := []string{"alice", "charlie", "dave"}
	if !reflect.DeepEqual(rc.Merged, want) {
		t.Errorf("Merged = %v, want %v", rc.Merged, want)
	}
	if orgMembers, ok := rc.Orgs["myorg"]; !ok {
		t.Error("expected myorg in Orgs")
	} else {
		if orgMembers.Role != "member" {
			t.Errorf("Role = %q, want %q", orgMembers.Role, "member")
		}
		wantMembers := []string{"charlie", "dave"}
		if !reflect.DeepEqual(orgMembers.Members, wantMembers) {
			t.Errorf("Members = %v, want %v", orgMembers.Members, wantMembers)
		}
	}
}

func TestResolveCommenters_Deduplicates(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script := writeOrgMembersScript(t, map[string]string{
		"myorg": "alice,charlie",
	})
	runner := gh.New(script)

	orgs := map[string]string{"myorg": "member"}
	rc := ResolveCommenters(context.Background(), runner, []string{"alice", "bob"}, orgs)

	want := []string{"alice", "bob", "charlie"}
	if !reflect.DeepEqual(rc.Merged, want) {
		t.Errorf("Merged = %v, want %v", rc.Merged, want)
	}
}

func TestResolveCommenters_OrgErrorSkipped(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	content := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	runner := gh.New(script)

	orgs := map[string]string{"badorg": "member"}
	rc := ResolveCommenters(context.Background(), runner, []string{"alice"}, orgs)

	want := []string{"alice"}
	if !reflect.DeepEqual(rc.Merged, want) {
		t.Errorf("Merged = %v, want %v", rc.Merged, want)
	}
	if len(rc.Orgs) != 0 {
		t.Errorf("expected empty Orgs on error, got %v", rc.Orgs)
	}
}

func TestResolveCommenters_MergedIsSorted(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	script := writeOrgMembersScript(t, map[string]string{
		"myorg": "zebra,alpha",
	})
	runner := gh.New(script)

	rc := ResolveCommenters(context.Background(), runner, []string{"mid"}, map[string]string{"myorg": "member"})

	if !sort.StringsAreSorted(rc.Merged) {
		t.Errorf("Merged is not sorted: %v", rc.Merged)
	}
}

func TestResolveCommenters_OwnerRole(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	content := `#!/bin/sh
ARGS="$*"
case "$ARGS" in
  *"role=admin"*) printf 'admin-user\n'; exit 0 ;;
  *"role=all"*)   printf 'admin-user\nmember-user\n'; exit 0 ;;
  *)              printf ''; exit 0 ;;
esac
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	runner := gh.New(script)

	rc := ResolveCommenters(context.Background(), runner, nil, map[string]string{"myorg": "owner"})

	want := []string{"admin-user"}
	if !reflect.DeepEqual(rc.Merged, want) {
		t.Errorf("Merged = %v, want %v (owner should map to role=admin)", rc.Merged, want)
	}
}

func TestWriteResolvedCommenters(t *testing.T) {
	dir := t.TempDir()
	rc := &ResolvedCommenters{
		ResolvedAt: "2026-01-01T00:00:00Z",
		Static:     []string{"alice"},
		Orgs: map[string]OrgMembers{
			"myorg": {Role: "member", Members: []string{"bob", "charlie"}},
		},
		Merged: []string{"alice", "bob", "charlie"},
	}

	if err := WriteResolvedCommenters(dir, rc); err != nil {
		t.Fatalf("WriteResolvedCommenters() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "resolved_commenters.yaml"))
	if err != nil {
		t.Fatalf("reading resolved file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "alice") {
		t.Errorf("expected alice in output, got:\n%s", content)
	}
	if !strings.Contains(content, "bob") {
		t.Errorf("expected bob in output, got:\n%s", content)
	}
	if !strings.Contains(content, "myorg") {
		t.Errorf("expected myorg in output, got:\n%s", content)
	}
}
