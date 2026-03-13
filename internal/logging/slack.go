package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// SlackHandler is an slog.Handler that posts log messages to a Slack webhook.
// Only messages at Info level or above are sent.
type SlackHandler struct {
	webhookURL string
	client     *http.Client
	attrs      []slog.Attr
	groups     []string
}

// NewSlackHandler creates a new SlackHandler that posts to the given webhook URL.
func NewSlackHandler(webhookURL string, client *http.Client) *SlackHandler {
	if client == nil {
		client = http.DefaultClient
	}
	return &SlackHandler{
		webhookURL: webhookURL,
		client:     client,
	}
}

func (h *SlackHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *SlackHandler) Handle(ctx context.Context, r slog.Record) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*[%s]* %s", r.Level.String(), r.Message)

	// Append pre-set attrs
	for _, a := range h.attrs {
		fmt.Fprintf(&sb, " | %s=%s", a.Key, a.Value.String())
	}

	// Append record attrs
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&sb, " | %s=%s", a.Key, a.Value.String())
		return true
	})

	payload := map[string]string{"text": sb.String()}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("posting to slack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned status %d", resp.StatusCode)
	}

	return nil
}

func (h *SlackHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SlackHandler{
		webhookURL: h.webhookURL,
		client:     h.client,
		attrs:      append(append([]slog.Attr{}, h.attrs...), attrs...),
		groups:     h.groups,
	}
}

func (h *SlackHandler) WithGroup(name string) slog.Handler {
	return &SlackHandler{
		webhookURL: h.webhookURL,
		client:     h.client,
		attrs:      h.attrs,
		groups:     append(append([]string{}, h.groups...), name),
	}
}

// MultiHandler fans out log records to multiple handlers.
type MultiHandler struct {
	handlers []slog.Handler
}

// NewMultiHandler creates a handler that writes to all provided handlers.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}

func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				fmt.Fprintf(os.Stderr, "logging: handler error: %v\n", err)
			}
		}
	}
	return nil
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: handlers}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &MultiHandler{handlers: handlers}
}

// SetupLogger creates a configured slog.Logger. When verbose is true, stderr
// gets Debug+ messages; otherwise Info+. If slackWebhookURL is non-empty,
// Slack always receives Info+ regardless of verbose.
func SetupLogger(slackWebhookURL string, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})

	if slackWebhookURL == "" {
		return slog.New(stderrHandler)
	}

	slackHandler := NewSlackHandler(slackWebhookURL, nil)
	return slog.New(NewMultiHandler(stderrHandler, slackHandler))
}
