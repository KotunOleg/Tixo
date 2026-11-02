package controllers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/notify"
)

const testWebhookSecret = "s3cret_value"

type recordingBot struct {
	updates []notify.Update
	err     error
}

func (b *recordingBot) HandleUpdate(_ context.Context, u notify.Update) error {
	b.updates = append(b.updates, u)
	return b.err
}

func webhookSite(t *testing.T, bot *recordingBot, secret string) http.Handler {
	t.Helper()
	return newSite(t, func(r chi.Router) {
		r.Method(http.MethodPost, "/test"+TelegramWebhookPath, TelegramWebhook{Bot: bot, Secret: secret, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	})
}

func postUpdate(t *testing.T, h http.Handler, secretHeader *string, body string) int {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test"+TelegramWebhookPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if secretHeader != nil {
		req.Header.Set(telegramWebhookAuthHeader, *secretHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestTelegramWebhookAcceptsOnlyTelegramWithTheSecret(t *testing.T) {
	update := `{"update_id":7,"message":{"chat":{"id":42,"type":"private"},"text":"/start"}}`
	right, wrong, empty := testWebhookSecret, "guess", ""
	cases := []struct {
		name       string
		configured string
		header     *string
		body       string
		wantStatus int
		wantCalls  int
	}{
		{"no secret header", testWebhookSecret, nil, update, http.StatusUnauthorized, 0},
		{"wrong secret", testWebhookSecret, &wrong, update, http.StatusUnauthorized, 0},
		{"empty header while secret configured", testWebhookSecret, &empty, update, http.StatusUnauthorized, 0},
		{"secret not configured lets nobody in", "", &empty, update, http.StatusUnauthorized, 0},
		{"right secret without browser origin headers", testWebhookSecret, &right, update, http.StatusOK, 1},
		{"broken json", testWebhookSecret, &right, `{"update_id":`, http.StatusBadRequest, 0},
		{"body over one megabyte", testWebhookSecret, &right, `{"update_id":1,"pad":"` + strings.Repeat("a", telegramWebhookMaxBodyBytes) + `"}`, http.StatusBadRequest, 0},
		{"update type the bot ignores", testWebhookSecret, &right, `{"update_id":8,"edited_message":{}}`, http.StatusOK, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bot := &recordingBot{}
			got := postUpdate(t, webhookSite(t, bot, tc.configured), tc.header, tc.body)
			if got != tc.wantStatus {
				t.Errorf("status = %d, want %d", got, tc.wantStatus)
			}
			if len(bot.updates) != tc.wantCalls {
				t.Errorf("bot handled %d updates, want %d", len(bot.updates), tc.wantCalls)
			}
		})
	}
}

func TestTelegramWebhookAnswers200WhenTheBotFailsSoTelegramDoesNotRetryForever(t *testing.T) {
	bot := &recordingBot{err: errors.New("database is down")}
	secret := testWebhookSecret
	if got := postUpdate(t, webhookSite(t, bot, secret), &secret, `{"update_id":9,"message":{"chat":{"id":1,"type":"private"},"text":"hi"}}`); got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}
}
