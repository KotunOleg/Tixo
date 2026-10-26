package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

const (
	signInHref = `href="/auth/google/login?next=`
	logoutForm = `action="/auth/logout"`
)

func TestNavbarShowsSignInOrAccount(t *testing.T) {
	s := newAuthSite(t)

	anon := s.request(t, http.MethodGet, "/", nil).Body.String()
	if !strings.Contains(anon, signInHref) || strings.Contains(anon, logoutForm) {
		t.Error("anonymous navbar: want a sign-in link and no logout form")
	}

	cookie := s.signIn(t)
	for _, path := range []string{"/", "/no-such-page"} {
		body := s.request(t, http.MethodGet, path, cookie).Body.String()
		if !strings.Contains(body, "Олена") || !strings.Contains(body, logoutForm) || strings.Contains(body, signInHref) {
			t.Errorf("%s for a signed-in user: want the user's name and a logout form instead of sign-in", path)
		}
		if !strings.Contains(body, `method="post" action="/auth/logout"`) {
			t.Errorf("%s: logout must be a POST form", path)
		}
	}
}

func TestNoSignInLinkWhenGoogleIsNotConfigured(t *testing.T) {
	body := do(t, newSite(t, nil), "/").Body.String()
	if strings.Contains(body, "/auth/google/login") {
		t.Error("navbar links to a sign-in that answers 404")
	}
}

func TestSessionOfDeletedUserBecomesAnonymous(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signIn(t)
	if _, err := s.pool.Exec(context.Background(), "DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	rec := s.request(t, http.MethodGet, "/", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "Олена") || !strings.Contains(body, signInHref) {
		t.Error("deleted user still shown as signed in")
	}
	if s.sessionUserID(t, cookie) != 0 {
		t.Error("user_id of a deleted user left in the session")
	}
}

func TestRequireUserSendsAnonymousToSignIn(t *testing.T) {
	s := newAuthSite(t)
	protected := i18n.Middleware(s.deps.Sessions.LoadAndSave(s.deps.loadUser()(requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.CurrentUser(r.Context())
		_, _ = w.Write([]byte("hello " + u.Name))
	})))))
	get := func(target string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		return rec
	}

	cases := map[string]string{
		"/org":            "/auth/google/login?next=%2Forg",
		"/en/org?tab=all": "/en/auth/google/login?next=%2Forg%3Ftab%3Dall",
	}
	for target, want := range cases {
		rec := get(target, nil)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("anonymous %s = %d → %q, want 302 → %q", target, rec.Code, rec.Header().Get("Location"), want)
		}
	}

	rec := get("/org", s.signIn(t))
	if rec.Code != http.StatusOK || rec.Body.String() != "hello Олена" {
		t.Errorf("signed-in /org = %d %q, want 200 with the current user", rec.Code, rec.Body.String())
	}
}

func TestCancelledSignInFlashIsShownOnce(t *testing.T) {
	s := newAuthSite(t)
	start := s.startLogin(t, "/auth/google/login")
	rec := s.request(t, http.MethodGet, auth.CallbackPath+"?error=access_denied&state="+start.state, start.cookie)
	cookie := responseCookie(rec)
	if cookie == nil {
		cookie = start.cookie
	}
	if body := s.request(t, http.MethodGet, "/", cookie).Body.String(); !strings.Contains(body, "Вхід скасовано.") {
		t.Error("cancel message not shown after returning from google")
	}
	if body := s.request(t, http.MethodGet, "/", cookie).Body.String(); strings.Contains(body, "Вхід скасовано.") {
		t.Error("cancel message shown again on the next page")
	}
}
