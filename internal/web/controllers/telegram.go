package controllers

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/notify"
)

const (
	TelegramWebhookPath         = "/tg/webhook"
	telegramWebhookAuthHeader   = "X-Telegram-Bot-Api-Secret-Token"
	telegramWebhookMaxBodyBytes = 1 << 20
)

type TelegramWebhook struct {
	Bot    notify.UpdateHandler
	Secret string
	Log    *slog.Logger
}

func (t TelegramWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !t.authorized(r.Header.Get(telegramWebhookAuthHeader)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var u notify.Update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, telegramWebhookMaxBodyBytes)).Decode(&u); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := t.Bot.HandleUpdate(r.Context(), u); err != nil {
		t.Log.ErrorContext(r.Context(), "telegram: handle update", "update_id", u.UpdateID, "error", err,
			"request_id", middleware.GetReqID(r.Context()))
	}
	w.WriteHeader(http.StatusOK)
}

func (t TelegramWebhook) authorized(got string) bool {
	return t.Secret != "" && subtle.ConstantTimeCompare([]byte(got), []byte(t.Secret)) == 1
}
