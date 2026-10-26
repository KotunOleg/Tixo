package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func setValidProdEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "prod")
	t.Setenv("BASE_URL", "https://tixo.example")
	t.Setenv("SESSION_SECRET", strings.Repeat("s", 32))
	t.Setenv("DATABASE_URL", "postgres://tixo:tixo@127.0.0.1:1/tixo?connect_timeout=1")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("AMQP_URL", "amqp://x")
	t.Setenv("GOOGLE_CLIENT_ID", "id.apps.googleusercontent.com")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret")
}

func TestMigrateResetRefusedInProd(t *testing.T) {
	setValidProdEnv(t)
	for _, cmd := range []string{"reset", "down"} {
		err := run(context.Background(), []string{"migrate", cmd}, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "refused in prod") {
			t.Errorf("migrate %s in prod: err = %v, want refusal", cmd, err)
		}
	}
}

func TestMigrateNeedsSubcommand(t *testing.T) {
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"migrate"}, &bytes.Buffer{}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "up|down|reset|status") {
		t.Errorf("err = %v, want usage", err)
	}
}
