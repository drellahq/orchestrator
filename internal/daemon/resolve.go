package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/goccy/go-yaml"

	gh "github.com/drellabot/orchestrator/internal/github"
)

// OrgMembers lists members fetched from a single GitHub organization.
type OrgMembers struct {
	Role    string   `yaml:"role"`
	Members []string `yaml:"members"`
}

// ResolvedCommenters holds the result of resolving allowed_commenters and
// allowed_commenters_orgs into a single merged list.
type ResolvedCommenters struct {
	ResolvedAt string                `yaml:"resolved_at"`
	Static     []string              `yaml:"static,omitempty"`
	Orgs       map[string]OrgMembers `yaml:"orgs,omitempty"`
	Merged     []string              `yaml:"merged"`
}

// ResolveCommenters fetches org members for each configured org and merges
// them with the static allowed_commenters list. Org API errors are logged
// as warnings and skipped so the daemon can still start.
func ResolveCommenters(ctx context.Context, ghRunner *gh.Runner, staticCommenters []string, orgs map[string]string) *ResolvedCommenters {
	rc := &ResolvedCommenters{
		ResolvedAt: time.Now().UTC().Format(time.RFC3339),
		Static:     staticCommenters,
		Orgs:       make(map[string]OrgMembers),
	}

	seen := make(map[string]bool, len(staticCommenters))
	for _, u := range staticCommenters {
		seen[u] = true
	}

	for org, role := range orgs {
		members, err := ghRunner.ListOrgMembers(ctx, org, role)
		if err != nil {
			slog.Warn("Failed to resolve org members, skipping", "org", org, "role", role, "error", err)
			continue
		}
		sort.Strings(members)
		rc.Orgs[org] = OrgMembers{Role: role, Members: members}
		for _, m := range members {
			seen[m] = true
		}
		slog.Info("Resolved org members", "org", org, "role", role, "count", len(members))
	}

	merged := make([]string, 0, len(seen))
	for u := range seen {
		merged = append(merged, u)
	}
	sort.Strings(merged)
	rc.Merged = merged
	return rc
}

// WriteResolvedCommenters writes the resolved commenters to a YAML file
// in the output directory for dashboard consumption.
func WriteResolvedCommenters(outputDir string, rc *ResolvedCommenters) error {
	data, err := yaml.Marshal(rc)
	if err != nil {
		return fmt.Errorf("marshaling resolved commenters: %w", err)
	}
	path := filepath.Join(outputDir, "resolved_commenters.yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
