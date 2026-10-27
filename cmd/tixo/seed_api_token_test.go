package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSeedAPITokenRefusedInProd(t *testing.T) {
	setValidProdEnv(t)
	err := run(context.Background(), []string{"seed-api-token"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errSeedAPITokenInProd) {
		t.Errorf("seed-api-token in prod: err = %v, want refusal", err)
	}
}

func TestSeedAPITokenRejectsUnexpectedArgsBeforeTouchingTheDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tixo:tixo@127.0.0.1:1/tixo?connect_timeout=1")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("AMQP_URL", "amqp://x")
	err := run(context.Background(), []string{"seed-api-token", "extra"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Errorf("seed-api-token extra: err = %v, want usage error", err)
	}
}
