package issueattachments

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxAttachmentCount = 20
	maxAttachmentBytes = 20 << 20 // 20 MiB
	sandboxDir         = "~/attachments/"
)

// userAttachmentsURL matches GitHub drag-and-drop attachment links in issue bodies.
var userAttachmentsURL = regexp.MustCompile(`https://github\.com/user-attachments/[^\s)\]"'<>]+`)

// DownloadedFile records a file saved on the host.
type DownloadedFile struct {
	Filename string // basename under attachments/
	LocalPath string
}

// IssueFetcher loads issue content and provides GitHub auth for downloads.
type IssueFetcher interface {
	FetchIssueBody(ctx context.Context, repo string, number int) (body string, err error)
	AuthToken(ctx context.Context) (string, error)
}

// AttachmentRef is a parsed user-attachment URL with a suggested local filename.
type AttachmentRef struct {
	URL      string
	Filename string
}

// ExtractURLs finds unique GitHub user-attachment URLs in markdown/text.
func ExtractURLs(body string) []AttachmentRef {
	seen := make(map[string]bool)
	var refs []AttachmentRef
	for _, m := range userAttachmentsURL.FindAllString(body, -1) {
		url := strings.TrimRight(m, ".,;:")
		if seen[url] {
			continue
		}
		seen[url] = true
		refs = append(refs, AttachmentRef{
			URL:      url,
			Filename: filenameFromURL(url),
		})
		if len(refs) >= maxAttachmentCount {
			break
		}
	}
	return refs
}

func filenameFromURL(url string) string {
	url = strings.TrimSuffix(url, "/")
	if i := strings.LastIndex(url, "/"); i >= 0 {
		name := url[i+1:]
		if name != "" {
			return sanitizeFilename(name)
		}
	}
	return "attachment"
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.Trim(b.String(), "._")
	if s == "" {
		return "attachment"
	}
	return s
}

// DownloadAll fetches each URL with a Bearer token. Failures are logged; successful
// downloads are returned. Returns an error only if destDir cannot be created.
func DownloadAll(ctx context.Context, token string, refs []AttachmentRef, destDir string) ([]DownloadedFile, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("creating attachments dir: %w", err)
	}

	client := &http.Client{}
	usedNames := make(map[string]int)
	var downloaded []DownloadedFile

	for _, ref := range refs {
		name := uniqueFilename(ref.Filename, ref.URL, usedNames)
		localPath := filepath.Join(destDir, name)

		if err := downloadOne(ctx, client, token, ref.URL, localPath); err != nil {
			slog.Warn("Failed to download issue attachment", "url", ref.URL, "error", err)
			continue
		}
		downloaded = append(downloaded, DownloadedFile{
			Filename:  name,
			LocalPath: localPath,
		})
	}
	return downloaded, nil
}

func uniqueFilename(base, url string, used map[string]int) string {
	name := base
	if used[name] > 0 {
		if id := attachmentIDFromURL(url); id != "" {
			ext := filepath.Ext(base)
			stem := strings.TrimSuffix(base, ext)
			name = fmt.Sprintf("%s_%s%s", id, stem, ext)
		}
	}
	for used[name] > 0 {
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		name = fmt.Sprintf("%s_%d%s", stem, used[base], ext)
	}
	used[base]++
	used[name]++
	return name
}

func attachmentIDFromURL(url string) string {
	// https://github.com/user-attachments/files/28067929/name.md
	const prefix = "https://github.com/user-attachments/files/"
	if !strings.HasPrefix(url, prefix) {
		prefix2 := "https://github.com/user-attachments/assets/"
		if strings.HasPrefix(url, prefix2) {
			rest := strings.TrimPrefix(url, prefix2)
			if i := strings.Index(rest, "/"); i > 0 {
				return rest[:i]
			}
			return rest
		}
		return ""
	}
	rest := strings.TrimPrefix(url, prefix)
	if i := strings.Index(rest, "/"); i > 0 {
		return rest[:i]
	}
	return ""
}

func downloadOne(ctx context.Context, client *http.Client, token, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// net/http drops Authorization if the client follows a 301/302 redirect
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxAttachmentBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxAttachmentBytes {
		return fmt.Errorf("attachment exceeds %d byte limit", maxAttachmentBytes)
	}

	return os.WriteFile(dest, data, 0644)
}

// Manifest returns markdown listing files available in the sandbox.
func Manifest(files []DownloadedFile) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n## Issue attachments\n\n")
	sb.WriteString("Files from the source GitHub issue are available under `~/attachments/`:\n\n")
	for _, f := range files {
		sb.WriteString("- `")
		sb.WriteString(f.Filename)
		sb.WriteString("`\n")
	}
	return sb.String()
}

// Sync downloads user-attachment files referenced in issueBody.
// If issueBody has no attachment URLs and sourceRepo/sourceIssue are set,
// the issue body is loaded via fetcher before scanning.
func Sync(ctx context.Context, fetcher IssueFetcher, issueBody, sourceRepo string, sourceIssue int, destDir string) ([]DownloadedFile, error) {
	refs := ExtractURLs(issueBody)

	if len(refs) == 0 && sourceRepo != "" && sourceIssue > 0 {
		fetched, err := fetcher.FetchIssueBody(ctx, sourceRepo, sourceIssue)
		if err != nil {
			return nil, fmt.Errorf("fetching issue for attachments: %w", err)
		}
		refs = ExtractURLs(fetched)
		slog.Debug("Re-fetched issue body for attachment URLs", "repo", sourceRepo, "issue", sourceIssue, "count", len(refs))
	}

	if len(refs) == 0 {
		return nil, nil
	}

	token, err := fetcher.AuthToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting auth token: %w", err)
	}

	slog.Info("Downloading issue attachments", "repo", sourceRepo, "issue", sourceIssue, "count", len(refs))
	return DownloadAll(ctx, token, refs, destDir)
}

// SandboxDir is the path inside the VM where attachments are copied.
func SandboxDir() string {
	return sandboxDir
}
