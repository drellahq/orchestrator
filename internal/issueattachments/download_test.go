package issueattachments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadAll(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		auth = r.Header.Get("Authorization")
		w.Write([]byte("# design doc\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	refs := []AttachmentRef{{URL: srv.URL + "/file.md", Filename: "design.md"}}
	files, err := DownloadAll(context.Background(), "secret-token", refs, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files", len(files))
	}
	if auth == "" {
		t.Error("expected Authorization header")
	}
	data, err := os.ReadFile(filepath.Join(dir, "design.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# design doc\n" {
		t.Errorf("content = %q", data)
	}
}

func TestDownloadAll_skipsFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	refs := []AttachmentRef{{URL: srv.URL, Filename: "missing.md"}}
	files, err := DownloadAll(context.Background(), "token", refs, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
}
