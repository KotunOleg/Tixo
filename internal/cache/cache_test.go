package cache

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func testStore(t *testing.T) (*Store, func()) {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	ns := "cache_test:" + t.Name()
	clean := func() {
		keys, _ := rdb.Keys(context.Background(), ns+"*").Result()
		if len(keys) > 0 {
			_ = rdb.Del(context.Background(), keys...).Err()
		}
		_ = rdb.Close()
	}
	return New(rdb, slog.New(slog.NewTextHandler(os.Stderr, nil))), clean
}

func TestFetchMissesThenHits(t *testing.T) {
	s, clean := testStore(t)
	defer clean()
	ns := "cache_test:" + t.Name()
	calls := 0
	load := func(context.Context) (string, error) {
		calls++
		return "value", nil
	}

	val, hit, err := Fetch(context.Background(), s, ns, "list", time.Minute, load)
	if err != nil || hit || val != "value" {
		t.Fatalf("want miss/value, got hit=%v val=%q err=%v", hit, val, err)
	}

	val, hit, err = Fetch(context.Background(), s, ns, "list", time.Minute, load)
	if err != nil || !hit || val != "value" {
		t.Fatalf("want hit/value, got hit=%v val=%q err=%v", hit, val, err)
	}
	if calls != 1 {
		t.Errorf("loader called %d times, want 1", calls)
	}
}

func TestBumpInvalidatesCache(t *testing.T) {
	s, clean := testStore(t)
	defer clean()
	ns := "cache_test:" + t.Name()
	calls := 0
	load := func(context.Context) (string, error) {
		calls++
		return "value", nil
	}

	if _, _, err := Fetch(context.Background(), s, ns, "list", time.Minute, load); err != nil {
		t.Fatal(err)
	}
	s.Bump(context.Background(), ns)

	_, hit, err := Fetch(context.Background(), s, ns, "list", time.Minute, load)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Error("expected miss after bump, got hit")
	}
	if calls != 2 {
		t.Errorf("loader called %d times, want 2", calls)
	}
}

func TestFetchFailsOpenWhenRedisIsDown(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	s := New(rdb, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	calls := 0
	load := func(context.Context) (string, error) {
		calls++
		return "value", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, hit, err := Fetch(ctx, s, "cache_test:down", "list", time.Minute, load)
	if err != nil || hit || val != "value" || calls != 1 {
		t.Fatalf("want fail-open miss/value with loader called once, got hit=%v val=%q err=%v calls=%d", hit, val, err, calls)
	}
}

func TestFetchPropagatesLoaderError(t *testing.T) {
	s, clean := testStore(t)
	defer clean()
	ns := "cache_test:" + t.Name()
	wantErr := errors.New("boom")
	load := func(context.Context) (string, error) {
		return "", wantErr
	}

	_, hit, err := Fetch(context.Background(), s, ns, "list", time.Minute, load)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want loader error, got %v", err)
	}
	if hit {
		t.Error("expected no hit on loader error")
	}
}
