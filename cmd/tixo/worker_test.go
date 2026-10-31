package main

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func setUnreachableWorkerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://tixo:tixo@127.0.0.1:1/tixo?connect_timeout=1")
	t.Setenv("REDIS_URL", "redis://127.0.0.1:1/0")
	t.Setenv("AMQP_URL", "amqp://guest:guest@127.0.0.1:1/")
}

func TestWorkerIsRegistered(t *testing.T) {
	if _, ok := commands["worker"]; !ok {
		t.Fatal(`commands["worker"] is not registered`)
	}
}

func TestWorkerFailsFastWhenRabbitMQIsUnreachable(t *testing.T) {
	setUnreachableWorkerEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	err := run(ctx, []string{"worker"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("worker started against an unreachable broker, want an error")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("worker ignored the context deadline instead of failing fast")
	}
}
