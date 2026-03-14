package logging

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func newTestConfig(url string) *SlackConfig {
	return &SlackConfig{
		BotToken:        "xoxb-test-token",
		Channel:         "C0123456789",
		TaskName:        "my-task",
		TaskDescription: "Fix the login bug",
	}
}

func TestSlackHandler(t *testing.T) {
	tests := []struct {
		name         string
		level        slog.Level
		msg          string
		attrs        []slog.Attr
		serverStatus int
		serverOK     *bool
		wantPost     bool
		wantSubstr   string
		wantErr      bool
	}{
		{
			name:       "info message is posted",
			level:      slog.LevelInfo,
			msg:        "Task started",
			attrs:      []slog.Attr{slog.String("task", "my-task")},
			wantPost:   true,
			wantSubstr: "task=my-task",
		},
		{
			name:       "warn message is posted",
			level:      slog.LevelWarn,
			msg:        "Something wrong",
			wantPost:   true,
			wantSubstr: "Something wrong",
		},
		{
			name:     "debug message is not posted",
			level:    slog.LevelDebug,
			msg:      "debug info",
			wantPost: false,
		},
		{
			name:         "server error returns error",
			level:        slog.LevelInfo,
			msg:          "test",
			serverStatus: http.StatusInternalServerError,
			wantPost:     true,
			wantErr:      true,
		},
		{
			name:       "slack API error returns error",
			level:      slog.LevelInfo,
			msg:        "test",
			serverOK:   boolPtr(false),
			wantPost:   true,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []slackRequest
			status := tt.serverStatus
			if status == 0 {
				status = http.StatusOK
			}
			apiOK := true
			if tt.serverOK != nil {
				apiOK = *tt.serverOK
			}

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer xoxb-test-token" {
					t.Errorf("Authorization = %q, want Bearer xoxb-test-token", got)
				}
				body, _ := io.ReadAll(r.Body)
				var req slackRequest
				_ = json.Unmarshal(body, &req)
				mu.Lock()
				requests = append(requests, req)
				mu.Unlock()

				w.WriteHeader(status)
				if status == http.StatusOK {
					resp := slackResponse{OK: apiOK, TS: "1234.5678"}
					if !apiOK {
						resp.Error = "channel_not_found"
					}
					_ = json.NewEncoder(w).Encode(resp)
				}
			}))
			defer ts.Close()

			// Override the Slack API URL by using a custom transport
			cfg := newTestConfig(ts.URL)
			h := NewSlackHandler(cfg, ts.Client())
			// Point the handler at our test server
			overrideSlackURL(t, ts.URL)

			if h.Enabled(context.Background(), tt.level) != tt.wantPost {
				t.Fatalf("Enabled(%v) = %v, want %v", tt.level, !tt.wantPost, tt.wantPost)
			}

			if !tt.wantPost {
				return
			}

			record := slog.NewRecord(time.Now(), tt.level, tt.msg, 0)
			for _, a := range tt.attrs {
				record.AddAttrs(a)
			}

			err := h.Handle(context.Background(), record)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Handle() error: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()

			// First request is the initial summary, second is the log record
			if len(requests) < 2 {
				t.Fatalf("expected at least 2 requests, got %d", len(requests))
			}

			// Initial message should not have thread_ts
			if requests[0].ThreadTS != "" {
				t.Errorf("initial message has thread_ts = %q, want empty", requests[0].ThreadTS)
			}
			if requests[0].Channel != "C0123456789" {
				t.Errorf("initial message channel = %q, want C0123456789", requests[0].Channel)
			}

			// Thread reply should have thread_ts
			lastReq := requests[len(requests)-1]
			if lastReq.ThreadTS != "1234.5678" {
				t.Errorf("thread reply thread_ts = %q, want 1234.5678", lastReq.ThreadTS)
			}

			if tt.wantSubstr != "" {
				found := false
				for _, req := range requests {
					if contains(req.Text, tt.wantSubstr) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no request text contains %q", tt.wantSubstr)
				}
			}
		})
	}
}

func TestSlackHandlerThreading(t *testing.T) {
	var mu sync.Mutex
	var requests []slackRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req slackRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(slackResponse{OK: true, TS: "9999.0001"})
	}))
	defer ts.Close()
	overrideSlackURL(t, ts.URL)

	cfg := newTestConfig(ts.URL)
	h := NewSlackHandler(cfg, ts.Client())

	for i, msg := range []string{"First", "Second", "Third"} {
		record := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
		if err := h.Handle(context.Background(), record); err != nil {
			t.Fatalf("Handle(%d) error: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// 1 initial summary + 3 log records = 4 requests
	if len(requests) != 4 {
		t.Fatalf("expected 4 requests, got %d", len(requests))
	}

	if requests[0].ThreadTS != "" {
		t.Errorf("initial message should have no thread_ts, got %q", requests[0].ThreadTS)
	}
	for i := 1; i < len(requests); i++ {
		if requests[i].ThreadTS != "9999.0001" {
			t.Errorf("request[%d] thread_ts = %q, want 9999.0001", i, requests[i].ThreadTS)
		}
	}
}

func TestSlackHandlerWithAttrsSharingThread(t *testing.T) {
	var mu sync.Mutex
	var requests []slackRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req slackRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(slackResponse{OK: true, TS: "5555.0001"})
	}))
	defer ts.Close()
	overrideSlackURL(t, ts.URL)

	cfg := newTestConfig(ts.URL)
	h := NewSlackHandler(cfg, ts.Client())

	// Post via original handler to establish thread
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "original", 0)
	if err := h.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}

	// Create a copy via WithAttrs and post — should reuse thread
	h2 := h.WithAttrs([]slog.Attr{slog.String("key", "val")})
	record2 := slog.NewRecord(time.Now(), slog.LevelInfo, "from copy", 0)
	if err := h2.Handle(context.Background(), record2); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	// initial + original + from-copy = 3
	if len(requests) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(requests))
	}
	if requests[2].ThreadTS != "5555.0001" {
		t.Errorf("WithAttrs copy thread_ts = %q, want 5555.0001", requests[2].ThreadTS)
	}
	if !contains(requests[2].Text, "key=val") {
		t.Errorf("WithAttrs copy should include attr, got %q", requests[2].Text)
	}
}

func TestMultiHandler(t *testing.T) {
	var infoCalled, debugCalled bool

	infoHandler := &trackingHandler{minLevel: slog.LevelInfo, called: &infoCalled}
	debugHandler := &trackingHandler{minLevel: slog.LevelDebug, called: &debugCalled}

	multi := NewMultiHandler(infoHandler, debugHandler)

	if !multi.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("MultiHandler should be enabled for Debug when one handler accepts it")
	}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	_ = multi.Handle(context.Background(), record)

	if !infoCalled {
		t.Error("info handler not called")
	}
	if !debugCalled {
		t.Error("debug handler not called for Info message")
	}
}

type trackingHandler struct {
	minLevel slog.Level
	called   *bool
}

func (h *trackingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}
func (h *trackingHandler) Handle(_ context.Context, _ slog.Record) error {
	*h.called = true
	return nil
}
func (h *trackingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *trackingHandler) WithGroup(_ string) slog.Handler      { return h }

func boolPtr(b bool) *bool { return &b }

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// overrideSlackURL temporarily replaces the package-level Slack URL for testing.
func overrideSlackURL(t *testing.T, url string) {
	t.Helper()
	old := slackPostMessageURL
	slackPostMessageURL = url
	t.Cleanup(func() { slackPostMessageURL = old })
}
