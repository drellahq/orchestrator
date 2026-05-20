package issueattachments

import (
	"strings"
	"testing"
)

func TestExtractURLs(t *testing.T) {
	body := `See the design:

https://github.com/user-attachments/files/28067929/2026-05-19-osbuild-friendly-stage-messages-design.md

Also [linked](https://github.com/user-attachments/files/111/doc.pdf) and duplicate:
https://github.com/user-attachments/files/28067929/2026-05-19-osbuild-friendly-stage-messages-design.md
`
	refs := ExtractURLs(body)
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	if refs[0].Filename != "2026-05-19-osbuild-friendly-stage-messages-design.md" {
		t.Errorf("filename[0] = %q", refs[0].Filename)
	}
	if refs[1].Filename != "doc.pdf" {
		t.Errorf("filename[1] = %q", refs[1].Filename)
	}
}

func TestExtractURLs_imageAsset(t *testing.T) {
	body := `![screenshot](https://github.com/user-attachments/assets/abc123/image.png)`
	refs := ExtractURLs(body)
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1", len(refs))
	}
	if refs[0].Filename != "image.png" {
		t.Errorf("filename = %q", refs[0].Filename)
	}
}

func TestExtractURLs_empty(t *testing.T) {
	if refs := ExtractURLs("no attachments here"); len(refs) != 0 {
		t.Fatalf("got %v", refs)
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename("../../../etc/passwd"); got != "passwd" {
		t.Errorf("got %q", got)
	}
}

func TestAttachmentIDFromURL(t *testing.T) {
	url := "https://github.com/user-attachments/files/28067929/foo.md"
	if id := attachmentIDFromURL(url); id != "28067929" {
		t.Errorf("id = %q", id)
	}
}

func TestManifest_empty(t *testing.T) {
	if Manifest(nil) != "" {
		t.Error("expected empty manifest")
	}
}

func TestManifest_listsFiles(t *testing.T) {
	m := Manifest([]DownloadedFile{{Filename: "a.md"}, {Filename: "b.pdf"}})
	for _, want := range []string{"a.md", "b.pdf", "~/attachments/"} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q: %q", want, m)
		}
	}
}
