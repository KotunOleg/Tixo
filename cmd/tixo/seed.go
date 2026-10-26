package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
	"github.com/KotunOleg/Tixo/internal/seed"
)

var errSeedInProd = errors.New("seed is refused in prod: it writes demo data")

func init() {
	commands["seed"] = command{summary: "load demo organizers, venues, events and artists", run: runSeed}
}

func runSeed(ctx context.Context, _ []string, stdout io.Writer) error {
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
	return nil
}
