package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2/memstore"
)

func TestSafeNextKeepsOnlyLocalPaths(t *testing.T) {
	cases := map[string]string{
		"":                                     "/",
		"/":                                    "/",
		"/events":                              "/events",
		"/en/org/kyiv-live?tab=ev":             "/en/org/kyiv-live?tab=ev",
		"//evil.com":                           "/",
		"///evil.com":                          "/",
		"/\\evil.com":                          "/",
		"/events\\..\\//evil.com":              "/",
		"https://evil.com":                     "/",
		"http:/evil.com":                       "/",
		"javascript:alert(1)":                  "/",
		"evil.com/path":                        "/",
		"/events\r\nSet-Cookie: x":             "/",
		"/events\tx":                           "/",
		"/auth/google/callback?code=c&state=s": "/",
		"/auth/google/login":                   "/",
		"/en/auth/google/callback":             "/",
		"/authors":                             "/authors",
	}
	for in, want := range cases {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPurchasePayloadIsPoppedOnce(t *testing.T) {
	base, _ := url.Parse("http://localhost:8080")
	sm := NewSessionsWithStore(memstore.NewWithCleanupInterval(0), *base, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	var first, firstOK, second, secondOK any
	h := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetPurchase(r.Context(), sm, `{"transaction_id":"42"}`)
		first, firstOK = PopPurchase(r.Context(), sm)
		second, secondOK = PopPurchase(r.Context(), sm)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if first != `{"transaction_id":"42"}` || firstOK != true {
		t.Errorf("first PopPurchase = (%v, %v), want the payload and true", first, firstOK)
	}
	if second != "" || secondOK != false {
		t.Errorf("second PopPurchase = (%v, %v), want empty and false: payload must be consumed once", second, secondOK)
	}
}

func TestGoogleClientHasATimeout(t *testing.T) {
	g := NewGoogle("id", "secret", url.URL{Scheme: "http", Host: "localhost:8080"})
	if g.client.Timeout <= 0 || g.client.Timeout > 30*time.Second {
		t.Errorf("google http client timeout = %s: a hung Google would hold the sign-in request forever", g.client.Timeout)
	}
}
