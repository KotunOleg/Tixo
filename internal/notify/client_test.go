package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:SECRET-bot-token"

func botServer(t *testing.T, status int, body string, seen func(r *http.Request, payload map[string]any)) *BotAPIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		if seen != nil {
			seen(r, payload)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewBotAPIClientWith(testToken, srv.URL, srv.Client())
}

func TestSendMessagePostsPlainTextWithoutParseMode(t *testing.T) {
	var path string
	var payload map[string]any
	c := botServer(t, http.StatusOK, `{"ok":true,"result":{}}`, func(r *http.Request, p map[string]any) {
		path, payload = r.URL.Path, p
	})

	text := "<b>Rock</b> [x](https://evil.example)"
	if err := c.SendMessage(context.Background(), 42, text); err != nil {
		t.Fatal(err)
	}
	if path != "/bot"+testToken+"/sendMessage" {
		t.Errorf("path = %q", path)
	}
	if payload["chat_id"] != float64(42) || payload["text"] != text {
		t.Errorf("payload = %v, want chat 42 and the text unchanged", payload)
	}
	if _, ok := payload["parse_mode"]; ok {
		t.Errorf("payload sets parse_mode, so event titles could inject markup: %v", payload)
	}
}

func TestBotAPIErrorsAreClassifiedForRetryDecisions(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"rate limited carries retry_after", http.StatusTooManyRequests,
			`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`,
			func(err error) bool {
				var ra *RetryAfterError
				return errors.As(err, &ra) && ra.After == 7*time.Second
			}},
		{"blocked by user", http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`,
			func(err error) bool { return errors.Is(err, ErrForbidden) }},
		{"webhook conflict", http.StatusConflict, `{"ok":false,"error_code":409,"description":"Conflict"}`,
			func(err error) bool { return errors.Is(err, ErrConflict) }},
		{"bad request is permanent", http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`,
			func(err error) bool {
				var pe *PermanentError
				return errors.As(err, &pe) && pe.Status == 400 && strings.Contains(pe.Description, "chat not found")
			}},
		{"server error is transient", http.StatusBadGateway, `<html>bad gateway</html>`,
			func(err error) bool {
				var pe *PermanentError
				var ra *RetryAfterError
				return err != nil && !errors.As(err, &pe) && !errors.As(err, &ra) && !errors.Is(err, ErrForbidden)
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := botServer(t, tc.status, tc.body, nil).SendMessage(context.Background(), 1, "hi")
			if !tc.check(err) {
				t.Errorf("err = %#v", err)
			}
			if err != nil && strings.Contains(err.Error(), testToken) {
				t.Errorf("error leaks the bot token: %v", err)
			}
		})
	}
}

func TestNetworkErrorDoesNotLeakTokenFromRequestURL(t *testing.T) {
	c := NewBotAPIClientWith(testToken, "http://127.0.0.1:1", &http.Client{})
	err := c.SendMessage(context.Background(), 1, "hi")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error leaks the bot token: %v", err)
	}
}

func TestGetUpdatesDecodesMessagesAndAsksOnlyForMessages(t *testing.T) {
	var payload map[string]any
	c := botServer(t, http.StatusOK,
		`{"ok":true,"result":[{"update_id":10,"message":{"chat":{"id":5,"type":"private"},"from":{"language_code":"en"},"text":"/start abc"}}]}`,
		func(_ *http.Request, p map[string]any) { payload = p })

	updates, err := c.GetUpdates(context.Background(), 9, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].UpdateID != 10 || updates[0].Message.Chat.ID != 5 ||
		updates[0].Message.From.LanguageCode != "en" || updates[0].Message.Text != "/start abc" {
		t.Errorf("updates = %+v", updates)
	}
	if payload["offset"] != float64(9) || payload["timeout"] != float64(2) {
		t.Errorf("payload = %v, want offset 9 and timeout 2", payload)
	}
	if allowed, _ := payload["allowed_updates"].([]any); len(allowed) != 1 || allowed[0] != "message" {
		t.Errorf("allowed_updates = %v, want only message", payload["allowed_updates"])
	}
}

func TestSetWebhookSendsSecretToken(t *testing.T) {
	var payload map[string]any
	c := botServer(t, http.StatusOK, `{"ok":true,"result":true}`, func(_ *http.Request, p map[string]any) { payload = p })
	if err := c.SetWebhook(context.Background(), "https://tixo.example/tg/webhook", "hook_secret"); err != nil {
		t.Fatal(err)
	}
	if payload["url"] != "https://tixo.example/tg/webhook" || payload["secret_token"] != "hook_secret" {
		t.Errorf("payload = %v", payload)
	}
}
