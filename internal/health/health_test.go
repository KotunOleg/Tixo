package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeChecker struct {
	name  string
	err   error
	delay time.Duration
}

func (f fakeChecker) Name() string { return f.name }

func (f fakeChecker) Check(ctx context.Context) error {
	select {
	case <-time.After(f.delay):
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type body struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func get(t *testing.T, h http.Handler) (int, body) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var b body
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("invalid JSON %q: %v", rec.Body.String(), err)
	}
	return rec.Code, b
}

func TestHealthStatus(t *testing.T) {
	cases := []struct {
		name       string
		checkers   []Checker
		wantCode   int
		wantStatus string
		wantChecks map[string]string
	}{
		{
			name:       "all dependencies up",
			checkers:   []Checker{fakeChecker{name: "postgres"}, fakeChecker{name: "redis"}},
			wantCode:   http.StatusOK,
			wantStatus: "ok",
			wantChecks: map[string]string{"postgres": "ok", "redis": "ok"},
		},
		{
			name:       "one dependency down names it",
			checkers:   []Checker{fakeChecker{name: "postgres"}, fakeChecker{name: "rabbitmq", err: errors.New("connection refused")}},
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "degraded",
			wantChecks: map[string]string{"postgres": "ok", "rabbitmq": "error"},
		},
		{
			name:       "hanging dependency counts as down instead of hanging the probe",
			checkers:   []Checker{fakeChecker{name: "redis", delay: time.Minute}},
			wantCode:   http.StatusServiceUnavailable,
			wantStatus: "degraded",
			wantChecks: map[string]string{"redis": "error"},
		},
		{
			name:       "no dependencies configured",
			wantCode:   http.StatusOK,
			wantStatus: "ok",
			wantChecks: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			code, b := get(t, Handler(50*time.Millisecond, tc.checkers...))
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("probe took %v, timeout not applied", elapsed)
			}
			if code != tc.wantCode || b.Status != tc.wantStatus {
				t.Errorf("got %d %q, want %d %q", code, b.Status, tc.wantCode, tc.wantStatus)
			}
			if len(b.Checks) != len(tc.wantChecks) {
				t.Errorf("checks = %v, want %v", b.Checks, tc.wantChecks)
			}
			for k, v := range tc.wantChecks {
				if b.Checks[k] != v {
					t.Errorf("checks[%s] = %q, want %q", k, b.Checks[k], v)
				}
			}
		})
	}
}

func TestHealthDoesNotLeakErrorDetails(t *testing.T) {
	secret := "password authentication failed for user tixo at 10.0.0.5"
	rec := httptest.NewRecorder()
	Handler(time.Second, fakeChecker{name: "postgres", err: errors.New(secret)}).
		ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if got := rec.Body.String(); strings.Contains(got, "10.0.0.5") {
		t.Errorf("public health response exposes dependency error: %s", got)
	}
}
