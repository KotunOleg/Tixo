package main

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestSchedulerIsRegistered(t *testing.T) {
	if _, ok := commands["scheduler"]; !ok {
		t.Fatal(`commands["scheduler"] is not registered`)
	}
}

func TestSchedulerFailsFastWhenRabbitMQIsUnreachable(t *testing.T) {
	setUnreachableWorkerEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	err := run(ctx, []string{"scheduler"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("scheduler started against an unreachable broker, want an error")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("scheduler ignored the context deadline instead of failing fast")
	}
}
