package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/seed"
)

var errSeedAPITokenInProd = errors.New("seed-api-token is refused in prod: it issues a working bearer token for an arbitrary email")

type seedAPITokenArgs struct {
	email, org, name string
}

func parseSeedAPITokenArgs(args []string) (seedAPITokenArgs, error) {
	fs := flag.NewFlagSet("seed-api-token", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a := seedAPITokenArgs{}
	fs.StringVar(&a.email, "email", "ci@example.com", "email of the bootstrap user")
	fs.StringVar(&a.org, "org", "qa1-ci", "slug of the organizer the user owns")
	fs.StringVar(&a.name, "name", "newman-ci", "display name for the issued api token")
	if err := fs.Parse(args); err != nil {
		return seedAPITokenArgs{}, fmt.Errorf("seed-api-token: %w", err)
	}
	if fs.NArg() > 0 {
		return seedAPITokenArgs{}, fmt.Errorf("seed-api-token: unexpected %q", fs.Arg(0))
	}
	return a, nil
}

func init() {
	commands["seed-api-token"] = command{
		summary: "create a bootstrap user+organizer and issue a bearer api token (CI/dev only)",
		run:     runSeedAPIToken,
	}
}

func runSeedAPIToken(ctx context.Context, args []string, stdout io.Writer) error {
	a, err := parseSeedAPITokenArgs(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProd() {
		return errSeedAPITokenInProd
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	userID, err := seed.EnsureAPIUser(ctx, pool, a.email, a.name, a.org, a.org)
	if err != nil {
		return fmt.Errorf("seed-api-token: %w", err)
	}
	raw, err := reissueAPIToken(ctx, apitokens.New(pool), userID, a.name)
	if err != nil {
		return fmt.Errorf("seed-api-token: %w", err)
	}
	fmt.Fprintf(stdout, "organizer: %s\n", a.org)
	fmt.Fprintf(stdout, "token: %s\n", raw)
	return nil
}

func reissueAPIToken(ctx context.Context, tokens *apitokens.Service, userID int64, name string) (string, error) {
	existing, err := tokens.List(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, t := range existing {
		if t.Name != strings.TrimSpace(name) {
			continue
		}
		if err := tokens.Revoke(ctx, userID, t.ID); err != nil {
			return "", fmt.Errorf("revoke stale token %d: %w", t.ID, err)
		}
	}
	raw, _, err := tokens.Issue(ctx, userID, name)
	return raw, err
}
