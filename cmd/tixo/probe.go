package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func init() {
	commands["probe"] = command{summary: "check the local server /healthz (container healthcheck)", run: runProbe}
}

func runProbe(ctx context.Context, _ []string, stdout io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL(os.Getenv("HTTP_ADDR")), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(stdout, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe: /healthz returned %d", resp.StatusCode)
	}
	return nil
}

func probeURL(addr string) string {
	port := uint64(8080)
	if _, raw, err := net.SplitHostPort(addr); err == nil {
		if n, err := strconv.ParseUint(raw, 10, 16); err == nil && n > 0 {
			port = n
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
}
