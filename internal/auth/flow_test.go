package auth

import "testing"

func TestSafeNextKeepsOnlyLocalPaths(t *testing.T) {
	cases := map[string]string{
		"":                         "/",
		"/":                        "/",
		"/events":                  "/events",
		"/en/org/kyiv-live?tab=ev": "/en/org/kyiv-live?tab=ev",
		"//evil.com":               "/",
		"///evil.com":              "/",
		"/\\evil.com":              "/",
		"/events\\..\\//evil.com":  "/",
		"https://evil.com":         "/",
		"http:/evil.com":           "/",
		"javascript:alert(1)":      "/",
		"evil.com/path":            "/",
		"/events\r\nSet-Cookie: x": "/",
		"/events\tx":               "/",
	}
	for in, want := range cases {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
