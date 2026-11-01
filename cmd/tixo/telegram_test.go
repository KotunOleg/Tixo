package main

import (
	"net/url"
	"testing"
	"time"

	"github.com/KotunOleg/Tixo/internal/config"
)

func TestParseRemindArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    time.Duration
		wantErr bool
	}{
		{"default is a day", nil, 24 * time.Hour, false},
		{"demo window", []string{"--within=72h"}, 72 * time.Hour, false},
		{"zero would queue nothing silently", []string{"--within=0"}, 0, true},
		{"negative", []string{"--within=-1h"}, 0, true},
		{"not a duration", []string{"--within=3"}, 0, true},
		{"stray argument", []string{"now"}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRemindArgs(tc.args)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("parseRemindArgs(%v) = %s, %v; want %s, error %v", tc.args, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestTelegramWebhookURL(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		secret  string
		want    string
		wantErr bool
	}{
		{"production", "https://tixo-service.duckdns.org", "s3cret_value", "https://tixo-service.duckdns.org/tg/webhook", false},
		{"trailing slash", "https://tixo.example/", "s3cret_value", "https://tixo.example/tg/webhook", false},
		{"plain http is refused by telegram", "http://localhost:8080", "s3cret_value", "", true},
		{"without a secret anyone could post updates", "https://tixo.example", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, err := url.Parse(tc.base)
			if err != nil {
				t.Fatal(err)
			}
			got, err := telegramWebhookURL(&config.Config{BaseURL: *base, TelegramWebhookSecret: tc.secret})
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("telegramWebhookURL = %q, %v; want %q, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestParseSetWebhookArgs(t *testing.T) {
	if remove, err := parseSetWebhookArgs(nil); err != nil || remove {
		t.Errorf("no args = %v, %v; want set", remove, err)
	}
	if remove, err := parseSetWebhookArgs([]string{"--delete"}); err != nil || !remove {
		t.Errorf("--delete = %v, %v; want remove", remove, err)
	}
	if _, err := parseSetWebhookArgs([]string{"https://evil.example"}); err == nil {
		t.Error("positional url accepted; the url must come from BASE_URL")
	}
}

func TestTelegramCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"telegram-remind", "telegram-poll", "telegram-set-webhook"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("command %q is not registered", name)
		}
	}
}
