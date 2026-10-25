package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/config"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/features"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
)

const flagsUsage = "usage: tixo flags enable|disable <flag> --org <slug> | tixo flags list --org <slug>"

type flagsArgs struct {
	action, name, org string
}

func parseFlagsArgs(args []string) (flagsArgs, error) {
	if len(args) == 0 {
		return flagsArgs{}, errors.New(flagsUsage)
	}
	a := flagsArgs{action: args[0]}
	fs := flag.NewFlagSet("flags", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.org, "org", "", "organizer slug")
	var positional []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return flagsArgs{}, fmt.Errorf("flags: %w", err)
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	a.org = strings.TrimSpace(a.org)
	if a.org == "" {
		return flagsArgs{}, fmt.Errorf("flags: --org is required; %s", flagsUsage)
	}
	switch a.action {
	case "enable", "disable":
		if len(positional) != 1 {
			return flagsArgs{}, fmt.Errorf("flags %s: want exactly one flag name; %s", a.action, flagsUsage)
		}
		a.name = positional[0]
	case "list":
		if len(positional) != 0 {
			return flagsArgs{}, fmt.Errorf("flags list: unexpected %q", positional[0])
		}
	default:
		return flagsArgs{}, fmt.Errorf("flags: unknown action %q; %s", a.action, flagsUsage)
	}
	return a, nil
}

func init() {
	commands["flags"] = command{
		summary: "list or toggle feature flags for an organizer without a deploy",
		run:     runFlagsCommand,
	}
}

func runFlagsCommand(ctx context.Context, args []string, stdout io.Writer) error {
	a, err := parseFlagsArgs(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()
	return runFlags(ctx, pool, a, stdout)
}

func runFlags(ctx context.Context, pool *pgxpool.Pool, a flagsArgs, stdout io.Writer) error {
	org, err := dbgen.New(pool).GetOrganizerBySlug(ctx, a.org)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("flags: organizer %q not found", a.org)
	}
	if err != nil {
		return err
	}
	svc := features.New(pool)
	switch a.action {
	case "enable":
		err = svc.Enable(ctx, org.ID, a.name)
	case "disable":
		err = svc.Disable(ctx, org.ID, a.name)
	}
	if err != nil {
		return fmt.Errorf("flags: %w", err)
	}
	states, err := svc.List(ctx, org.ID)
	if err != nil {
		return err
	}
	for _, s := range states {
		if a.name != "" && s.Name != a.name {
			continue
		}
		fmt.Fprintf(stdout, "%s %s %s\n", org.Slug, s.Name, describeFlag(s))
	}
	return nil
}

func describeFlag(s features.State) string {
	if !s.Enabled() {
		return "off"
	}
	var why []string
	if s.EnabledForAll {
		why = append(why, "all")
	}
	if s.ByPlan {
		why = append(why, "plan")
	}
	if s.ByOverride {
		why = append(why, "override")
	}
	return "on (" + strings.Join(why, ", ") + ")"
}
