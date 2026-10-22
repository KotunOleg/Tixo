package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/seed"
)

var (
	errSeedInProd = errors.New("seed is refused in prod: it writes demo data")
	errSeedUsage  = errors.New("usage: tixo seed [--owner <demo-organizer-slug>=<email>], --owner may repeat")
)

type ownerFlags []seed.Owner

func (o *ownerFlags) String() string { return fmt.Sprint(*o) }

func (o *ownerFlags) Set(raw string) error {
	owner, err := seed.ParseOwner(raw)
	if err != nil {
		return err
	}
	*o = append(*o, owner)
	return nil
}

func parseSeedArgs(args []string) ([]seed.Owner, error) {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var owners ownerFlags
	fs.Var(&owners, "owner", "give an email owner access to a demo organizer")
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("%w: %w", errSeedUsage, err)
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("%w: unexpected %q", errSeedUsage, fs.Arg(0))
	}
	return owners, nil
}

func init() {
	commands["seed"] = command{summary: "load demo organizers, venues, events and artists", run: runSeed}
}

func runSeed(ctx context.Context, args []string, stdout io.Writer) error {
	owners, err := parseSeedArgs(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProd() {
		return errSeedInProd
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	rep, err := seed.Run(ctx, pool, time.Now())
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	fmt.Fprintln(stdout, rep)
	if len(owners) == 0 {
		return nil
	}
	if err := seed.AddOwners(ctx, pool, owners); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	for _, o := range owners {
		fmt.Fprintf(stdout, "owner of %s: %s (sign in with this Google account)\n", o.Slug, o.Email)
	}
	return nil
}
