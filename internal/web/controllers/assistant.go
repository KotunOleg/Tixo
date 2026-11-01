package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/agent"
	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

const (
	assistantBodyLimit = 16 << 10
	assistantTurnLimit = 2 * time.Minute
)

type Assistant struct {
	Agent  *agent.Agent
	Limits *agent.Limits
	Log    *slog.Logger
	Now    func() time.Time
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
	w.WriteHeader(http.StatusNoContent)
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
