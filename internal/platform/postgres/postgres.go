package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/health"
)

var ErrNotMigrated = errors.New("database schema is not migrated")

func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DATABASE_URL: %w", err)
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	return pool, nil
}

func Checker(pool *pgxpool.Pool) health.Checker {
	q := dbgen.New(pool)
	return health.CheckFunc{
		CheckerName: "postgres",
		Fn: func(ctx context.Context) error {
			migrated, err := q.SchemaStatus(ctx)
			if err != nil {
				return err
			}
			if !migrated {
				return ErrNotMigrated
			}
			return nil
		},
	}
}
