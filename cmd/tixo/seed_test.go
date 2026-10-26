package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestSeedRefusedInProd(t *testing.T) {
	setValidProdEnv(t)
	err := run(context.Background(), []string{"seed"}, &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errSeedInProd) {
		t.Errorf("seed in prod: err = %v, want refusal", err)
	}
}

func TestSeedRejectsBadOwnersBeforeTouchingTheDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tixo:tixo@127.0.0.1:1/tixo?connect_timeout=1")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("AMQP_URL", "amqp://x")
	for _, args := range [][]string{
		{"seed", "--owner", "my-own-org=me@gmail.com"},
		{"seed", "--owner", "kyiv-live"},
		{"seed", "extra"},
	} {
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{})
		if !errors.Is(err, errSeedUsage) {
			t.Errorf("%v: err = %v, want usage error", args, err)
		}
	}
}
