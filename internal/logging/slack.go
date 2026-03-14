package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
)

var slackPostMessageURL = "https://slack.com/api/chat.postMessage"

// SlackConfig holds the information needed to post threaded Slack messages.
type SlackConfig struct {
	BotToken        string
	Channel         string
	TaskName        string
	TaskDescription string
}

// threadState is shared across handler copies created by WithAttrs/WithGroup
// so that all messages thread onto the same parent.
type threadState struct {
	mu       sync.Mutex
	parentTS string
}

// SlackHandler is an slog.Handler that posts log messages to a Slack channel
// using the Slack Web API (chat.postMessage) with a bot token. The first
// message posts a task summary to the channel; subsequent messages are
// threaded as replies.
type SlackHandler struct {
	config *SlackConfig
	client *http.Client
	state  *threadState
	attrs  []slog.Attr
	groups []string
}

// NewSlackHandler creates a new SlackHandler that posts to the given channel
// using a bot token. The first message will be a task summary; all further
// messages become thread replies.
func NewSlackHandler(cfg *SlackConfig, client *http.Client) *SlackHandler {
	if client == nil {
		client = http.DefaultClient
	}
	return &SlackHandler{
		config: cfg,
		client: client,
		state:  &threadState{},
	}
}

func (h *SlackHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

type slackRequest struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

type slackResponse struct {
	OK    bool   `json:"ok"`
	TS    string `json:"ts"`
	Error string `json:"error"`
}

func (h *SlackHandler) Handle(ctx context.Context, r slog.Record) error {
	h.state.mu.Lock()
	needsInitial := h.state.parentTS == ""
	threadTS := h.state.parentTS
	h.state.mu.Unlock()

	if needsInitial {
		ts, err := h.postInitialMessage(ctx)
		if err != nil {
			return fmt.Errorf("posting initial slack message: %w", err)
		}
		h.state.mu.Lock()
		h.state.parentTS = ts
		threadTS = ts
		h.state.mu.Unlock()
	}

	text := h.formatRecord(r)
	return h.post(ctx, text, threadTS)
}

func (h *SlackHandler) postInitialMessage(ctx context.Context) (string, error) {
	text := fmt.Sprintf("*Task: %s*\n> %s", h.config.TaskName, h.config.TaskDescription)
	return h.postAndGetTS(ctx, text, "")
}

func (h *SlackHandler) post(ctx context.Context, text, threadTS string) error {
	_, err := h.postAndGetTS(ctx, text, threadTS)
	return err
}

func (h *SlackHandler) postAndGetTS(ctx context.Context, text, threadTS string) (string, error) {
	payload := slackRequest{
		Channel:  h.config.Channel,
		Text:     text,
		ThreadTS: threadTS,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackPostMessageURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+h.config.BotToken)

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("posting to slack: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading slack response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("slack returned status %d: %s", resp.StatusCode, respBody)
	}

	var slackResp slackResponse
	if err := json.Unmarshal(respBody, &slackResp); err != nil {
		return "", fmt.Errorf("decoding slack response: %w", err)
	}
	if !slackResp.OK {
		return "", fmt.Errorf("slack API error: %s", slackResp.Error)
	}

	return slackResp.TS, nil
}

func (h *SlackHandler) formatRecord(r slog.Record) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*[%s]* %s", r.Level.String(), r.Message)

	for _, a := range h.attrs {
		fmt.Fprintf(&sb, " | %s=%s", a.Key, a.Value.String())
	}

	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&sb, " | %s=%s", a.Key, a.Value.String())
		return true
	})

	return sb.String()
}

func (h *SlackHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SlackHandler{
		config: h.config,
		client: h.client,
		state:  h.state,
		attrs:  append(append([]slog.Attr{}, h.attrs...), attrs...),
		groups: h.groups,
	}
}

func (h *SlackHandler) WithGroup(name string) slog.Handler {
	return &SlackHandler{
		config: h.config,
		client: h.client,
		state:  h.state,
		attrs:  h.attrs,
		groups: append(append([]string{}, h.groups...), name),
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
// gets Debug+ messages; otherwise Info+. If a SlackConfig is provided with
// both BotToken and Channel set, Slack always receives Info+ regardless of
// verbose, with all updates threaded under an initial summary message.
func SetupLogger(slack *SlackConfig, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})

	if slack == nil || slack.BotToken == "" || slack.Channel == "" {
		return slog.New(stderrHandler)
	}

	slackHandler := NewSlackHandler(slack, nil)
	return slog.New(NewMultiHandler(stderrHandler, slackHandler))
}
