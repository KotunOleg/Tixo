package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunUnknownCommandShowsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"srve"}, &stdout, &stderr)
	if !errors.Is(err, errUsage) {
		t.Fatalf("err = %v, want errUsage", err)
	}
	if !strings.Contains(stderr.String(), `unknown command "srve"`) || !strings.Contains(stderr.String(), "version") {
		t.Errorf("stderr lacks error or command list:\n%s", stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != version {
		t.Errorf("stdout = %q, want %q", stdout.String(), version)
	}
}
