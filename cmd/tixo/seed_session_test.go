package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	"github.com/KotunOleg/Tixo/internal/platform/redisclient"
	"github.com/KotunOleg/Tixo/internal/seed"
)

func TestSeedSessionRefusedInProd(t *testing.T) {
	setValidProdEnv(t)
	err := run(context.Background(), []string{"seed-session"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errSeedSessionInProd) {
		t.Errorf("seed-session in prod: err = %v, want refusal", err)
	}
}

func TestSeedSessionRejectsUnexpectedArgsBeforeTouchingTheDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tixo:tixo@127.0.0.1:1/tixo?connect_timeout=1")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("AMQP_URL", "amqp://x")
	err := run(context.Background(), []string{"seed-session", "extra"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Errorf("seed-session extra: err = %v, want usage error", err)
	}
}

func TestIssueSessionTokenLogsInTheGivenUserInRedis(t *testing.T) {
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	pool := dbtest.Pool(t)
	ctx := context.Background()
	userID, err := seed.EnsureAPIUser(ctx, pool, "me@example.com", "e2e-ci", "e2e-ci", "e2e-ci")
	if err != nil {
		t.Fatal(err)
	}

	rdb, err := redisclient.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	base, err := url.Parse("http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BaseURL: *base}

	token, err := issueSessionToken(ctx, rdb, cfg, userID)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("issueSessionToken returned an empty token")
	}
	t.Cleanup(func() { _ = rdb.Del(ctx, "tixo:session:"+token).Err() })

	sm := auth.NewSessions(rdb, cfg.BaseURL, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	sctx, err := sm.Load(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := auth.UserID(sctx, sm)
	if !ok || got != userID {
		t.Errorf("UserID from loaded token = %d, %v, want %d, true: seed-session did not sign the user in", got, ok, userID)
	}
}
