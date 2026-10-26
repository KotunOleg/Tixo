package dbtest

import (
	"context"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
)

var counter atomic.Int64

func BaseURL(t testing.TB) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set; start postgres from compose to run integration tests")
	}
	if err := RequireTestDatabase(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func RequireTestDatabase(raw string) error {
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

func EmptyURL(t testing.TB) string {
	t.Helper()
	base := BaseURL(t)
	ctx := context.Background()

	u, err := neturl.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%s_%d_%d", strings.TrimPrefix(u.Path, "/"), os.Getpid(), counter.Add(1))
	ident := pgx.Identifier{name}.Sanitize()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect to %s: %v", u.Redacted(), err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Logf("drop %s: %v", name, err)
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})

	u.Path = "/" + name
	return u.String()
}

func MigratedURL(t testing.TB) string {
	t.Helper()
	url := EmptyURL(t)
	if err := db.Migrate(context.Background(), url, "up", io.Discard); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	return url
}

func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), MigratedURL(t), 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
