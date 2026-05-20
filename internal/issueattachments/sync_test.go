package issueattachments

import (
	"context"
	"errors"
	"testing"
)

type stubFetcher struct {
	fetchBody  string
	fetchErr   error
	token      string
	fetchCalls int
}

func (s *stubFetcher) FetchIssueBody(ctx context.Context, repo string, number int) (string, error) {
	s.fetchCalls++
	if s.fetchErr != nil {
		return "", s.fetchErr
	}
	return s.fetchBody, nil
}

func (s *stubFetcher) AuthToken(ctx context.Context) (string, error) {
	return s.token, nil
}

func TestSync_noURLs_noSource(t *testing.T) {
	dir := t.TempDir()
	f := &stubFetcher{token: "tok"}
	files, err := Sync(context.Background(), f, "plain issue text", "", 0, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
	if f.fetchCalls != 0 {
		t.Errorf("FetchIssueBody called %d times, want 0", f.fetchCalls)
	}
}

func TestSync_bodyHasURLs_noFetch(t *testing.T) {
	dir := t.TempDir()
	body := "See https://github.com/user-attachments/files/99/design.md"
	f := &stubFetcher{token: "tok", fetchBody: "should not be used"}
	_, err := Sync(context.Background(), f, body, "org/tasks", 42, dir)
	// Download may fail without network; we only assert fetch was skipped.
	if f.fetchCalls != 0 {
		t.Errorf("FetchIssueBody called %d times, want 0 when body has URLs", f.fetchCalls)
	}
	_ = err
}

func TestSync_fallbackFetch_called(t *testing.T) {
	dir := t.TempDir()
	f := &stubFetcher{
		token:     "tok",
		fetchBody: "https://github.com/user-attachments/files/1/x.md",
	}
	_, _ = Sync(context.Background(), f, "short prompt", "org/tasks", 7, dir)
	if f.fetchCalls != 1 {
		t.Fatalf("FetchIssueBody calls = %d, want 1", f.fetchCalls)
	}
}

func TestSync_fetchError(t *testing.T) {
	dir := t.TempDir()
	f := &stubFetcher{fetchErr: errors.New("api down")}
	_, err := Sync(context.Background(), f, "no urls", "org/tasks", 1, dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if f.fetchCalls != 1 {
		t.Errorf("fetch calls = %d", f.fetchCalls)
	}
}
