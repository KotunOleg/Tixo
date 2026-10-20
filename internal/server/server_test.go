package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func newTestHandler(t *testing.T, logs *bytes.Buffer, mount func(r chi.Router)) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(logs, nil))
	return New(Deps{
		Logger: log,
		Health: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		Routes: mount,
	})
}

func TestRequestIDHeader(t *testing.T) {
	h := newTestHandler(t, &bytes.Buffer{}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d", rec.Code)
	}
	id := rec.Header().Get("X-Request-Id")
	if id == "" {
		t.Fatal("response has no X-Request-Id, logs cannot be correlated with user reports")
	}
	host, _ := os.Hostname()
	if host != "" && strings.Contains(id, host) {
		t.Errorf("request id %q exposes the server host name", id)
	}
}

func TestClientCannotChooseRequestID(t *testing.T) {
	h := newTestHandler(t, &bytes.Buffer{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "forged\nlevel=ERROR msg=fake")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-Id"); strings.Contains(got, "forged") {
		t.Errorf("client-supplied request id %q was trusted", got)
	}
}

func TestPanicBecomes500AndIsLogged(t *testing.T) {
	var logs bytes.Buffer
	h := newTestHandler(t, &logs, func(r chi.Router) {
		r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("nil map write") })
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "nil map write") {
		t.Error("panic message leaked to the client")
	}
	if !strings.Contains(logs.String(), "nil map write") {
		t.Errorf("panic not logged:\n%s", logs.String())
	}
}

func TestCrossSiteFormPostIsRejected(t *testing.T) {
	called := false
	h := newTestHandler(t, &bytes.Buffer{}, func(r chi.Router) {
		r.Post("/events/x/buy", func(w http.ResponseWriter, _ *http.Request) { called = true })
	})
	req := httptest.NewRequest(http.MethodPost, "/events/x/buy", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called || rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST reached handler=%v status=%d, want blocked with 403", called, rec.Code)
	}
}

func TestRunWaitsForInFlightRequestOnShutdown(t *testing.T) {
	started := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- Serve(ctx, ln, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Second)
	}()

	respBody := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			respBody <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		respBody <- string(b)
	}()

	<-started
	cancel()
	if got := <-respBody; got != "done" {
		t.Errorf("in-flight request got %q, want it to finish during shutdown", got)
	}
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Serve returned %v", err)
	}
}
