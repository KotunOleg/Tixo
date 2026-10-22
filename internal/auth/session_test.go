package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/alexedwards/scs/v2/memstore"

	"github.com/KotunOleg/Tixo/internal/platform/redisclient"
)

func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			return c
		}
	}
	t.Fatalf("response has no %s cookie", SessionCookieName)
	return nil
}

func TestSessionCookieFlags(t *testing.T) {
	cases := []struct {
		baseURL    string
		wantSecure bool
	}{
		{"http://localhost:8080", false},
		{"https://tixo.me", true},
	}
	for _, tc := range cases {
		t.Run(tc.baseURL, func(t *testing.T) {
			base, err := url.Parse(tc.baseURL)
			if err != nil {
				t.Fatal(err)
			}
			sm := NewSessionsWithStore(memstore.NewWithCleanupInterval(0), *base, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			h := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sm.Put(r.Context(), "k", "v")
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

			c := sessionCookie(t, rec.Result())
			if !c.HttpOnly {
				t.Error("session cookie readable from JavaScript: HttpOnly not set")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax: Strict drops the cookie on the Google callback, None sends it cross-site", c.SameSite)
			}
			if c.Secure != tc.wantSecure {
				t.Errorf("Secure = %v, want %v for %s", c.Secure, tc.wantSecure, tc.baseURL)
			}
		})
	}
}

func TestSessionSurvivesBetweenRequestsInRedis(t *testing.T) {
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	rdb, err := redisclient.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	base, _ := url.Parse("http://localhost:8080")
	sm := NewSessions(rdb, *base, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	put := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), "user_id", int64(42))
	}))
	rec := httptest.NewRecorder()
	put.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	cookie := sessionCookie(t, rec.Result())
	t.Cleanup(func() { _ = rdb.Del(t.Context(), sessionKeyPrefix+cookie.Value).Err() })

	var got int64
	get := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = sm.GetInt64(r.Context(), "user_id")
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	get.ServeHTTP(httptest.NewRecorder(), req)
	if got != 42 {
		t.Errorf("user_id from the next request = %d, want 42: session is not stored in redis", got)
	}
	n, err := rdb.Exists(t.Context(), sessionKeyPrefix+cookie.Value).Result()
	if err != nil || n != 1 {
		t.Errorf("redis key %s exists = %d, %v", sessionKeyPrefix+cookie.Value, n, err)
	}
}
