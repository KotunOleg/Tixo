package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbe(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"healthy", http.StatusOK, false},
		{"degraded", http.StatusServiceUnavailable, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("probe hit %s, want /healthz", r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			t.Setenv("HTTP_ADDR", strings.TrimPrefix(srv.URL, "http://"))
			err := run(context.Background(), []string{"probe"}, &bytes.Buffer{}, &bytes.Buffer{})
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestProbeAddress(t *testing.T) {
	cases := map[string]string{
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:8080":   "http://127.0.0.1:8080/healthz",
		"127.0.0.1:9000": "http://127.0.0.1:9000/healthz",
		"":               "http://127.0.0.1:8080/healthz",
	}
	for addr, want := range cases {
		if got := probeURL(addr); got != want {
			t.Errorf("probeURL(%q) = %q, want %q", addr, got, want)
		}
	}
}
