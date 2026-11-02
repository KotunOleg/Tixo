package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/agent"
	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

const (
	assistantBodyLimit = 16 << 10
	assistantTurnLimit = 2 * time.Minute
)

type Assistant struct {
	View   *view.Renderer
	Agent  *agent.Agent
	Limits *agent.Limits
	Log    *slog.Logger
	Now    func() time.Time
}

var assistantTextKeys = []string{
	"assistant.ai_label", "assistant.you", "assistant.pending", "assistant.steps", "assistant.thinking", "assistant.stopped",
	"assistant.truncated", "assistant.retry", "assistant.from", "assistant.free", "assistant.results", "assistant.failed",
	"assistant.cancelled_message", "assistant.empty_title", "assistant.suggest_1", "assistant.suggest_2", "assistant.suggest_3",
	"assistant.tool.search_events", "assistant.tool.get_event", "assistant.tool.list_venues", "assistant.tool.book_ticket",
	"assistant.error.network", "assistant.error.busy", "assistant.error.rate", "assistant.error.session",
	"assistant.error.refusal", "assistant.error.too_many_steps", "assistant.error.invalid", "nav.sign_in",
	"assistant.confirm.title", "assistant.confirm.event", "assistant.confirm.date", "assistant.confirm.tier",
	"assistant.confirm.quantity", "assistant.confirm.total", "assistant.confirm.test_payment",
	"assistant.confirm.buy", "assistant.confirm.cancel",
}

type assistantPage struct {
	Transcript  []agent.Line
	Suggestions []string
	Texts       map[string]string
	ChatURL     string
	ResetURL    string
	LoginURL    string
	EventsURL   string
}

func (c Assistant) Page(w http.ResponseWriter, r *http.Request) {
	p := c.View.PageFor(r)
	data := assistantPage{
		ChatURL: p.URL("/assistant/chat"), ResetURL: p.URL("/assistant/reset"), EventsURL: p.URL("/events/"),
		LoginURL: p.URL("/auth/google/login") + "?next=" + url.QueryEscape(p.URL("/assistant")),
		Texts:    make(map[string]string, len(assistantTextKeys)),
	}
	for _, key := range assistantTextKeys {
		text, err := p.T(key)
		if err != nil {
			c.pageError(w, r, err)
			return
		}
		data.Texts[key] = text
	}
	data.Suggestions = []string{data.Texts["assistant.suggest_1"], data.Texts["assistant.suggest_2"], data.Texts["assistant.suggest_3"]}
	if u, ok := auth.CurrentUser(r.Context()); ok && c.Agent != nil {
		lines, err := c.Agent.Transcript(r.Context(), u.ID)
		if err != nil {
			c.pageError(w, r, err)
			return
		}
		data.Transcript = lines
	}
	c.View.Render(w, r, http.StatusOK, "pages/assistant", data)
}

func (c Assistant) pageError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "assistant page failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}

type chatRequest struct {
	Message string `json:"message"`
}

type rateLimitBody struct {
	Error   string    `json:"error"`
	Limit   int       `json:"limit"`
	RetryAt time.Time `json:"retry_at"`
}

func (c Assistant) Chat(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "sign in to use the assistant", nil)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, assistantBodyLimit)).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "body must be JSON with a message", nil)
		return
	}
	text, err := agent.CheckMessage(req.Message)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), map[string]string{"message": "length"})
		return
	}

	release, err := c.Limits.Acquire(r.Context(), u.ID, c.Now())
	var limited *agent.RateLimited
	switch {
	case errors.Is(err, agent.ErrBusy):
		httpx.WriteError(w, http.StatusConflict, "busy", nil)
		return
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(limited.RetryAt).Seconds()))))
		httpx.WriteJSON(w, http.StatusTooManyRequests, rateLimitBody{Error: "rate limited", Limit: limited.Limit, RetryAt: limited.RetryAt})
		return
	case err != nil:
		c.Log.ErrorContext(r.Context(), "assistant limits failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	defer release()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := sseWriter{w: w, rc: http.NewResponseController(w)}
	if err := stream.rc.Flush(); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), assistantTurnLimit)
	defer cancel()
	lang := string(i18n.FromContext(r.Context()))
	if err := c.Agent.Run(ctx, u.ID, lang, text, stream.emit); err != nil && ctx.Err() == nil && !errors.Is(err, errStreamClosed) {
		c.Log.ErrorContext(r.Context(), "assistant turn ended with error", "error", err, "request_id", middleware.GetReqID(r.Context()))
	}
}

func (c Assistant) Reset(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.CurrentUser(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "sign in to use the assistant", nil)
		return
	}
	if err := c.Agent.Reset(r.Context(), u.ID); err != nil {
		c.Log.ErrorContext(r.Context(), "assistant reset failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

var errStreamClosed = errors.New("assistant stream closed by the client")

type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s sseWriter) emit(event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode %s event: %w", event, err)
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return errStreamClosed
	}
	if err := s.rc.Flush(); err != nil {
		return errStreamClosed
	}
	return nil
}
