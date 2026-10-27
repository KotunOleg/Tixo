package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	"github.com/KotunOleg/Tixo/internal/seed"
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

func TestSeedAPITokenKeepsTheUsersOtherTokens(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	userID, err := seed.EnsureAPIUser(ctx, pool, "me@example.com", "newman-ci", "qa1-ci", "qa1-ci")
	if err != nil {
		t.Fatal(err)
	}
	tokens := apitokens.New(pool)
	personal, _, err := tokens.Issue(ctx, userID, "postman on my laptop")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := reissueAPIToken(ctx, tokens, userID, "newman-ci")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := reissueAPIToken(ctx, tokens, userID, "newman-ci")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		raw   string
		valid bool
	}{
		{"personal token survives reseed", personal, true},
		{"previous seeded token is revoked", stale, false},
		{"fresh seeded token works", fresh, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tokens.Resolve(ctx, tc.raw)
			if got := err == nil; got != tc.valid {
				t.Errorf("Resolve err = %v, want valid=%v", err, tc.valid)
			}
		})
	}
}
