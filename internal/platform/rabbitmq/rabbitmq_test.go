package rabbitmq

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCheckerFailsFastWhenBrokerIsDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Checker("amqp://guest:guest@127.0.0.1:1/").Check(ctx); err == nil {
		t.Error("check passed with no broker")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("check ignored the context deadline")
	}
}

func TestCheckerDoesNotLeakCredentials(t *testing.T) {
	err := Checker("amqp://tixo:s3cret-pass@127.0.0.1:1/").Check(context.Background())
	if err != nil && strings.Contains(err.Error(), "s3cret-pass") {
		t.Errorf("error exposes broker password: %v", err)
	}
}

func TestCheckerAgainstRealBroker(t *testing.T) {
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Checker(url).Check(ctx); err != nil {
		t.Errorf("check failed against live broker: %v", err)
	}
}
