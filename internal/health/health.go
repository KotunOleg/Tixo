package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

type CheckFunc struct {
	CheckerName string
	Fn          func(ctx context.Context) error
}

func (c CheckFunc) Name() string                    { return c.CheckerName }
func (c CheckFunc) Check(ctx context.Context) error { return c.Fn(ctx) }

type response struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func Handler(timeout time.Duration, checkers ...Checker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		resp := response{Status: "ok", Checks: make(map[string]string, len(checkers))}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, c := range checkers {
			wg.Go(func() {
				err := c.Check(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					slog.WarnContext(r.Context(), "health check failed", "dependency", c.Name(), "err", err)
					resp.Checks[c.Name()] = "error"
					resp.Status = "degraded"
					return
				}
				resp.Checks[c.Name()] = "ok"
			})
		}
		wg.Wait()

		code := http.StatusOK
		if resp.Status != "ok" {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)
	})
}
