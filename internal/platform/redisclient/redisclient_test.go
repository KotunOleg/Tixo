package redisclient

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCheckerFailsFastWhenRedisIsDown(t *testing.T) {
	client, err := New("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Checker(client).Check(ctx); err == nil {
		t.Error("check passed with no redis")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("check ignored the context deadline")
	}
}

func TestNewRejectsInvalidURL(t *testing.T) {
	if _, err := New("not a url"); err == nil {
		t.Error("invalid REDIS_URL accepted")
	}
}

func TestCheckerAgainstRealRedis(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	client, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := Checker(client).Check(context.Background()); err != nil {
		t.Errorf("check failed against live redis: %v", err)
	}
}
