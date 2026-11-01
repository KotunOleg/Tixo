package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	botAPIBaseURL   = "https://api.telegram.org"
	requestTimeout  = 10 * time.Second
	maxResponseBody = 1 << 20
)

var (
	ErrForbidden = errors.New("telegram: the bot cannot write to this chat")
	ErrConflict  = errors.New("telegram: getUpdates is not allowed while a webhook is set")
)

type RetryAfterError struct {
	After time.Duration
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("telegram: rate limited, retry after %s", e.After)
}

type PermanentError struct {
	Status      int
	Description string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("telegram: request rejected with status %d: %s", e.Status, e.Description)
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	Chat Chat   `json:"chat"`
	From *User  `json:"from,omitempty"`
	Text string `json:"text"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type User struct {
	LanguageCode string `json:"language_code"`
}

type Client interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
	GetUpdates(ctx context.Context, offset int64, wait time.Duration) ([]Update, error)
	SetWebhook(ctx context.Context, webhookURL, secret string) error
}

type BotAPIClient struct {
	token   string
	baseURL string
	client  *http.Client
}

func NewBotAPIClient(token string) *BotAPIClient {
	return NewBotAPIClientWith(token, botAPIBaseURL, &http.Client{})
}

func NewBotAPIClientWith(token, baseURL string, client *http.Client) *BotAPIClient {
	return &BotAPIClient{token: token, baseURL: baseURL, client: client}
}

type sendMessageBody struct {
	ChatID                int64  `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type getUpdatesBody struct {
	Offset         int64    `json:"offset"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

type setWebhookBody struct {
	URL            string   `json:"url"`
	SecretToken    string   `json:"secret_token"`
	AllowedUpdates []string `json:"allowed_updates"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (b *BotAPIClient) SendMessage(ctx context.Context, chatID int64, text string) error {
	_, err := b.call(ctx, "sendMessage", sendMessageBody{ChatID: chatID, Text: text, DisableWebPagePreview: true}, requestTimeout)
	return err
}

func (b *BotAPIClient) GetUpdates(ctx context.Context, offset int64, wait time.Duration) ([]Update, error) {
	body := getUpdatesBody{Offset: offset, Timeout: int(wait / time.Second), AllowedUpdates: []string{"message"}}
	raw, err := b.call(ctx, "getUpdates", body, requestTimeout+wait)
	if err != nil {
		return nil, err
	}
	var updates []Update
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, fmt.Errorf("telegram: decode getUpdates result: %w", err)
	}
	return updates, nil
}

func (b *BotAPIClient) SetWebhook(ctx context.Context, webhookURL, secret string) error {
	_, err := b.call(ctx, "setWebhook", setWebhookBody{URL: webhookURL, SecretToken: secret, AllowedUpdates: []string{"message"}}, requestTimeout)
	return err
}

func (b *BotAPIClient) call(ctx context.Context, method string, payload any, timeout time.Duration) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("telegram: %s: marshal: %w", method, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/bot"+b.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("telegram: %s: build request failed", method)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("telegram: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&out); err != nil && resp.StatusCode < http.StatusInternalServerError {
		return nil, fmt.Errorf("telegram: %s: decode response with status %d: %w", method, resp.StatusCode, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK && out.OK:
		return out.Result, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &RetryAfterError{After: time.Duration(out.Parameters.RetryAfter) * time.Second}
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrForbidden
	case resp.StatusCode == http.StatusConflict:
		return nil, ErrConflict
	case resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError:
		return nil, &PermanentError{Status: resp.StatusCode, Description: out.Description}
	default:
		return nil, fmt.Errorf("telegram: %s: status %d", method, resp.StatusCode)
	}
}

type DryRunClient struct {
	Log *slog.Logger
}

func (d *DryRunClient) SendMessage(_ context.Context, chatID int64, text string) error {
	d.Log.Info("telegram: dry-run sendMessage", "chat_id", chatID, "text", text)
	return nil
}

func (d *DryRunClient) GetUpdates(ctx context.Context, _ int64, wait time.Duration) ([]Update, error) {
	d.Log.Info("telegram: dry-run getUpdates returns nothing; set TELEGRAM_DRY_RUN=false to poll the real bot")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(wait):
		return nil, nil
	}
}

func (d *DryRunClient) SetWebhook(_ context.Context, webhookURL, _ string) error {
	d.Log.Info("telegram: dry-run setWebhook", "url", webhookURL)
	return nil
}
