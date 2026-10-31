package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/platform/redisclient"
	"github.com/KotunOleg/Tixo/internal/seed"
)

var errSeedSessionInProd = errors.New("seed-session is refused in prod: it mints a working logged-in cookie for an arbitrary email")

type seedSessionArgs struct {
	email, org, name string
}

func parseSeedSessionArgs(args []string) (seedSessionArgs, error) {
	fs := flag.NewFlagSet("seed-session", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a := seedSessionArgs{}
	fs.StringVar(&a.email, "email", "e2e@example.com", "email of the bootstrap user")
	fs.StringVar(&a.org, "org", "e2e-ci", "slug of the organizer the user owns")
	fs.StringVar(&a.name, "name", "e2e-ci", "display name for the bootstrap user")
	if err := fs.Parse(args); err != nil {
		return seedSessionArgs{}, fmt.Errorf("seed-session: %w", err)
	}
	if fs.NArg() > 0 {
		return seedSessionArgs{}, fmt.Errorf("seed-session: unexpected %q", fs.Arg(0))
	}
	return a, nil
}

func init() {
	commands["seed-session"] = command{
		summary: "create a bootstrap user+organizer and print a logged-in session cookie (CI/dev only)",
		run:     runSeedSession,
	}
}

func runSeedSession(ctx context.Context, args []string, stdout io.Writer) error {
	a, err := parseSeedSessionArgs(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProd() {
		return errSeedSessionInProd
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	userID, err := seed.EnsureAPIUser(ctx, pool, a.email, a.name, a.org, a.org)
	if err != nil {
		return fmt.Errorf("seed-session: %w", err)
	}

	rdb, err := redisclient.New(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("seed-session: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	token, err := issueSessionToken(ctx, rdb, cfg, userID)
	if err != nil {
		return fmt.Errorf("seed-session: %w", err)
	}

	fmt.Fprintf(stdout, "organizer: %s\n", a.org)
	fmt.Fprintf(stdout, "cookie: %s=%s\n", auth.SessionCookieName, token)
	return nil
}

func issueSessionToken(ctx context.Context, rdb *redis.Client, cfg *config.Config, userID int64) (string, error) {
	sm := auth.NewSessions(rdb, cfg.BaseURL, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	sctx, err := sm.Load(ctx, "")
	if err != nil {
		return "", err
	}
	if err := auth.SignIn(sctx, sm, userID); err != nil {
		return "", err
	}
	token, _, err := sm.Commit(sctx)
	return token, err
}
