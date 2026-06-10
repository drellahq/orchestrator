package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

const attachmentReleaseTag = "_image-attachments"

// UploadImage uploads an image as a GitHub release asset and returns the
// browser_download_url that can be used inline in markdown comments.
// The image is stored in a draft release tagged "_image-attachments".
func (r *Runner) UploadImage(ctx context.Context, repo, filename string, data []byte) (string, error) {
	token, err := r.AuthToken(ctx)
	if err != nil {
		return "", fmt.Errorf("getting auth token for upload: %w", err)
	}

	releaseID, err := r.ensureAttachmentRelease(ctx, repo)
	if err != nil {
		return "", err
	}

	uniqueName := uniqueAssetName(filename, data)
	contentType := contentTypeFromExt(filename)

	return r.uploadReleaseAsset(ctx, token, repo, releaseID, uniqueName, contentType, data)
}

// ensureAttachmentRelease finds or creates the draft release used for image
// attachments. It returns the release ID.
func (r *Runner) ensureAttachmentRelease(ctx context.Context, repo string) (int64, error) {
	endpoint := fmt.Sprintf("/repos/%s/releases", repo)
	out, err := r.run(ctx, "", r.bin, "api", endpoint,
		"--jq", fmt.Sprintf(`.[] | select(.tag_name == %q) | .id`, attachmentReleaseTag))
	if err == nil {
		if id, parseErr := strconv.ParseInt(strings.TrimSpace(out), 10, 64); parseErr == nil && id > 0 {
			return id, nil
		}
	}

	out, err = r.run(ctx, "", r.bin, "api", "--method", "POST", endpoint,
		"-f", "tag_name="+attachmentReleaseTag,
		"-f", "name=Image Attachments",
		"-f", "body=Automatically created for inline image attachments.",
		"-F", "draft=true",
		"--jq", ".id")
	if err != nil {
		return 0, fmt.Errorf("creating attachment release on %s: %w", repo, err)
	}
	id, parseErr := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if parseErr != nil {
		return 0, fmt.Errorf("parsing release ID from %q: %w", strings.TrimSpace(out), parseErr)
	}
	return id, nil
}

type releaseAssetResponse struct {
	BrowserDownloadURL string `json:"browser_download_url"`
}

// uploadReleaseAsset POSTs binary data directly to the GitHub uploads endpoint.
func (r *Runner) uploadReleaseAsset(ctx context.Context, token, repo string, releaseID int64, name, contentType string, data []byte) (string, error) {
	uploadURL := fmt.Sprintf("https://uploads.github.com/repos/%s/releases/%d/assets?name=%s",
		repo, releaseID, url.QueryEscape(name))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, strings.NewReader(string(data)))
	if err != nil {
		return "", fmt.Errorf("creating upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(data))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("uploading release asset: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload returned HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var asset releaseAssetResponse
	if err := json.Unmarshal(body, &asset); err != nil {
		return "", fmt.Errorf("parsing upload response: %w", err)
	}
	if asset.BrowserDownloadURL == "" {
		return "", fmt.Errorf("upload response missing browser_download_url")
	}
	return asset.BrowserDownloadURL, nil
}

func uniqueAssetName(filename string, data []byte) string {
	hash := sha256.Sum256(data)
	prefix := fmt.Sprintf("%x", hash[:4])
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filepath.Base(filename), ext)
	return prefix + "_" + stem + ext
}

func contentTypeFromExt(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
