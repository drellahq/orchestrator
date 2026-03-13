package logging

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSlackHandler(t *testing.T) {
	tests := []struct {
		name         string
		level        slog.Level
		msg          string
		attrs        []slog.Attr
		serverStatus int // 0 means 200
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var received string
			status := tt.serverStatus
			if status == 0 {
				status = http.StatusOK
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received = string(body)
				w.WriteHeader(status)
			}))
			defer ts.Close()

			h := NewSlackHandler(ts.URL, ts.Client())

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

			if received == "" {
				t.Fatal("no HTTP request received")
			}

			var payload map[string]string
			if err := json.Unmarshal([]byte(received), &payload); err != nil {
				t.Fatalf("invalid JSON payload: %v", err)
			}

			text := payload["text"]
			if text == "" {
				t.Fatal("empty text in payload")
			}

			if tt.wantSubstr != "" && !strings.Contains(text, tt.wantSubstr) {
				t.Errorf("payload text %q does not contain %q", text, tt.wantSubstr)
			}
		})
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
