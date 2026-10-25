package db

import (
	"bytes"
	"context"
	"fmt"
	neturl "net/url"
	"os"
	"strings"
	"testing"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set; start postgres from compose to run integration tests")
	}
	if err := requireTestDatabase(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func requireTestDatabase(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("TEST_DATABASE_URL: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.Contains(name, "test") {
		return fmt.Errorf("TEST_DATABASE_URL points to database %q; integration tests drop the schema, so the database name must contain \"test\"", name)
	}
	return nil
}

func TestRefusesNonTestDatabase(t *testing.T) {
	cases := map[string]bool{
		"postgres://tixo:tixo@localhost:55432/tixo_test?sslmode=disable": true,
		"postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable":      false,
		"postgres://u:p@prod-host:5432/ecommerce":                        false,
	}
	for raw, ok := range cases {
		if err := requireTestDatabase(raw); (err == nil) != ok {
			t.Errorf("%s: err = %v, want allowed=%v", raw, err, ok)
		}
	}
}

func TestMigrationsRoundTrip(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	var out bytes.Buffer

	if err := Migrate(ctx, url, "reset", &out); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := Migrate(ctx, url, "up", &out); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := Migrate(ctx, url, "up", &out); err != nil {
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

	if err := Migrate(ctx, url, "reset", &out); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if ok, _ := q.SchemaStatus(ctx); ok {
		t.Error("down migration left pg_trgm installed")
	}
	err = postgres.Checker(pool).Check(ctx)
	if err == nil || !strings.Contains(err.Error(), "migrat") {
		t.Errorf("health check on an unmigrated database = %v, want a 'not migrated' error", err)
	}

	if err := Migrate(ctx, url, "status", &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := Migrate(ctx, url, "up", &out); err != nil {
		t.Fatalf("up after reset: %v", err)
	}
}

func TestMigrateRejectsUnknownCommand(t *testing.T) {
	err := Migrate(context.Background(), "postgres://unused", "sideways", &bytes.Buffer{})
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
