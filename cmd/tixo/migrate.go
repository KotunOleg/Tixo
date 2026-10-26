package main

import (
	"context"
	"fmt"
	"io"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/db"
)

func init() {
	commands["migrate"] = command{summary: "apply database migrations: up|down|reset|status", run: runMigrate}
}

func runMigrate(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("migrate: expected one of up|down|reset|status")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProd() && (args[0] == "reset" || args[0] == "down") {
		return fmt.Errorf("migrate %s is refused in prod: it drops data", args[0])
	}
	return db.Migrate(ctx, cfg.DatabaseURL, args[0], stdout)
}
