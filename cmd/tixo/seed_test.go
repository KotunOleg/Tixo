package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSeedRefusedInProd(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("BASE_URL", "https://tixo.example")
	t.Setenv("SESSION_SECRET", strings.Repeat("s", 32))
	t.Setenv("DATABASE_URL", "postgres://tixo:tixo@127.0.0.1:1/tixo?connect_timeout=1")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("AMQP_URL", "amqp://x")
	err := run(context.Background(), []string{"seed"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errSeedInProd) {
		t.Errorf("seed in prod: err = %v, want refusal", err)
	}
}
