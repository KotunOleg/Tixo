package auth

import (
	"net/url"
	"testing"
	"time"
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

func TestGoogleClientHasATimeout(t *testing.T) {
	g := NewGoogle("id", "secret", url.URL{Scheme: "http", Host: "localhost:8080"})
	if g.client.Timeout <= 0 || g.client.Timeout > 30*time.Second {
		t.Errorf("google http client timeout = %s: a hung Google would hold the sign-in request forever", g.client.Timeout)
	}
}
