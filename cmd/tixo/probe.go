package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
	defer resp.Body.Close()
	_, _ = io.Copy(stdout, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe: /healthz returned %d", resp.StatusCode)
	}
	return nil
}

func probeURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}
