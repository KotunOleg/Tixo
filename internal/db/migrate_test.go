package db_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/db"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
)

func TestMigrationsRoundTrip(t *testing.T) {
	url := dbtest.EmptyURL(t)
	ctx := context.Background()
	var out bytes.Buffer

	if err := db.Migrate(ctx, url, "reset", &out); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := db.Migrate(ctx, url, "up", &out); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := db.Migrate(ctx, url, "up", &out); err != nil {
		t.Fatalf("second up must be a no-op, got: %v", err)
	}

	pool, err := postgres.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := dbgen.New(pool)

	if ok, err := q.SchemaStatus(ctx); err != nil || !ok {
		t.Fatalf("after up: trgm_installed=%v err=%v", ok, err)
	}
	if err := postgres.Checker(pool).Check(ctx); err != nil {
		t.Errorf("health check fails on a migrated database: %v", err)
	}

	if err := db.Migrate(ctx, url, "reset", &out); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if ok, _ := q.SchemaStatus(ctx); ok {
		t.Error("down migration left pg_trgm installed")
	}
	err = postgres.Checker(pool).Check(ctx)
	if err == nil || !strings.Contains(err.Error(), "migrat") {
		t.Errorf("health check on an unmigrated database = %v, want a 'not migrated' error", err)
	}

	if err := db.Migrate(ctx, url, "status", &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := db.Migrate(ctx, url, "up", &out); err != nil {
		t.Fatalf("up after reset: %v", err)
	}
}

func TestMigrateRejectsUnknownCommand(t *testing.T) {
	err := db.Migrate(context.Background(), "postgres://unused", "sideways", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "sideways") {
		t.Errorf("err = %v, want unknown command error", err)
	}
}

func TestHealthCheckReportsUnreachableDatabase(t *testing.T) {
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, "postgres://tixo:tixo@127.0.0.1:1/tixo?sslmode=disable&connect_timeout=1", 2)
	if err != nil {
		t.Fatalf("pool creation must not dial eagerly: %v", err)
	}
	defer pool.Close()
	if err := postgres.Checker(pool).Check(ctx); err == nil {
		t.Error("health check passed with no database")
	}
}
