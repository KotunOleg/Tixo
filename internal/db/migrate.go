package db

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/KotunOleg/Tixo/internal/db/migrations"
)

func Migrate(ctx context.Context, databaseURL, command string, out io.Writer) error {
	switch command {
	case "up", "down", "reset", "status":
	default:
		return fmt.Errorf("migrate: unknown command %q (use up|down|reset|status)", command)
	}

	conn, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer conn.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	switch command {
	case "up":
		results, err := provider.Up(ctx)
		for _, r := range results {
			fmt.Fprintln(out, r)
		}
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		if len(results) == 0 {
			fmt.Fprintln(out, "no pending migrations")
		}
	case "down":
		r, err := provider.Down(ctx)
		if r != nil {
			fmt.Fprintln(out, r)
		}
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
	case "reset":
		results, err := provider.DownTo(ctx, 0)
		for _, r := range results {
			fmt.Fprintln(out, r)
		}
		if err != nil {
			return fmt.Errorf("migrate reset: %w", err)
		}
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		for _, s := range statuses {
			applied := "pending"
			if !s.AppliedAt.IsZero() {
				applied = s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(out, "%-8s %-40s %s\n", s.State, s.Source.Path, applied)
		}
	}
	return nil
}
