package controllers

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/users"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type fakeGoogle struct {
	srv         *httptest.Server
	mu          sync.Mutex
	challenges  map[string]string
	profile     map[string]any
	tokenStatus int
	tokenDelay  time.Duration
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{
		challenges: map[string]string{},
		profile:    map[string]any{"sub": "sub-olena", "email": "olena@example.com", "email_verified": true, "name": "Олена"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("GET /userinfo", f.userinfo)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) issueCode(code, challenge string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenges[code] = challenge
}

func (f *fakeGoogle) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	status, delay := f.tokenStatus, f.tokenDelay
	code := r.PostForm.Get("code")
	challenge, ok := f.challenges[code]
	delete(f.challenges, code)
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !ok || oauth2.S256ChallengeFromVerifier(r.PostForm.Get("code_verifier")) != challenge ||
		r.PostForm.Get("redirect_uri") != "https://tixo.example"+auth.CallbackPath {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	_, _ = w.Write([]byte(`{"access_token":"at-` + code + `","token_type":"Bearer","expires_in":3600}`))
}

func (f *fakeGoogle) userinfo(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at-") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(f.profile)
}

type authSite struct {
	h    http.Handler
	deps Deps
	fake *fakeGoogle
	svc  *users.Service
	pool *pgxpool.Pool
}

func newAuthSite(t *testing.T) *authSite {
	t.Helper()
	return newAuthSiteWithClient(t, nil)
}

func newAuthSiteWithClient(t *testing.T, client *http.Client) *authSite {
	t.Helper()
	return newAuthSiteWith(t, client, nil)
}

func newAuthSiteWith(t *testing.T, client *http.Client, extra func(r chi.Router)) *authSite {
	t.Helper()
	fake := newFakeGoogle(t)
	if client == nil {
		client = fake.srv.Client()
	}
	pool := dbtest.Pool(t)
	svc := users.New(pool)
	h, d := buildSite(t, func(d *Deps) {
		d.Users = svc
		d.Organizers = organizers.New(pool)
		d.Venues = venues.New(pool)
		d.Events = events.New(pool)
		store, err := storage.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		d.Storage = store
		d.Google = auth.NewGoogleWith("client-id", "client-secret", testBaseURL, auth.GoogleEndpoints{
			Auth:     oauth2.Endpoint{AuthURL: fake.srv.URL + "/authorize", TokenURL: fake.srv.URL + "/token"},
			Userinfo: fake.srv.URL + "/userinfo",
		}, client)
	}, extra)
	return &authSite{h: h, deps: d, fake: fake, svc: svc, pool: pool}
}

func (s *authSite) signInAs(t *testing.T, sub, email, name string) *http.Cookie {
	t.Helper()
	s.fake.mu.Lock()
	s.fake.profile = map[string]any{"sub": sub, "email": email, "email_verified": true, "name": name}
	s.fake.mu.Unlock()
	return s.signIn(t)
}

func (s *authSite) signIn(t *testing.T) *http.Cookie {
	t.Helper()
	start := s.startLogin(t, "/auth/google/login")
	code := "code-" + start.state
	s.fake.issueCode(code, start.challenge)
	rec := s.request(t, http.MethodGet, callbackURL(code, start.state), start.cookie)
	cookie := responseCookie(rec)
	if rec.Code != http.StatusFound || cookie == nil || s.sessionUserID(t, cookie) == 0 {
		t.Fatalf("sign-in failed: %d", rec.Code)
	}
	return cookie
}

func (s *authSite) request(t *testing.T, method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func responseCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	return nil
}

type loginStart struct {
	cookie    *http.Cookie
	state     string
	challenge string
	redirect  *url.URL
}

func (s *authSite) startLogin(t *testing.T, target string) loginStart {
	t.Helper()
	resp := s.request(t, http.MethodGet, target, nil)
	if resp.Code != http.StatusFound {
		t.Fatalf("GET %s = %d, want 302 to google", target, resp.Code)
	}
	loc, err := url.Parse(resp.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cookie := responseCookie(resp)
	if cookie == nil {
		t.Fatal("login start set no session cookie: state cannot be checked on callback")
	}
	return loginStart{cookie: cookie, state: loc.Query().Get("state"), challenge: loc.Query().Get("code_challenge"), redirect: loc}
}

func (s *authSite) sessionUserID(t *testing.T, cookie *http.Cookie) int64 {
	t.Helper()
	ctx, err := s.deps.Sessions.Load(context.Background(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := auth.UserID(ctx, s.deps.Sessions)
	return id
}

func callbackURL(code, state string) string {
	return auth.CallbackPath + "?" + url.Values{"code": {code}, "state": {state}}.Encode()
}

func TestLoginRedirectsToGoogleWithStateAndPKCE(t *testing.T) {
	s := newAuthSite(t)
	start := s.startLogin(t, "/auth/google/login")
	q := start.redirect.Query()
	checks := map[string]string{
		"client_id":             "client-id",
		"redirect_uri":          "https://tixo.example" + auth.CallbackPath,
		"response_type":         "code",
		"code_challenge_method": "S256",
		"prompt":                "select_account",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if len(start.state) < 20 || len(start.challenge) < 40 {
		t.Errorf("state %q / code_challenge %q look too weak", start.state, start.challenge)
	}
	if !strings.Contains(q.Get("scope"), "email") {
		t.Errorf("scope %q does not ask for email", q.Get("scope"))
	}
}

func TestGoogleSignInRegistersUserAndRenewsSession(t *testing.T) {
	s := newAuthSite(t)
	start := s.startLogin(t, "/en/auth/google/login?next=/events")
	s.fake.issueCode("code-1", start.challenge)

	resp := s.request(t, http.MethodGet, callbackURL("code-1", start.state), start.cookie)
	if resp.Code != http.StatusFound || resp.Header().Get("Location") != "/en/events" {
		t.Fatalf("callback = %d → %q, want 302 → /en/events", resp.Code, resp.Header().Get("Location"))
	}
	signedIn := responseCookie(resp)
	if signedIn == nil || signedIn.Value == start.cookie.Value {
		t.Fatal("session token not renewed on sign-in: session fixation")
	}
	id := s.sessionUserID(t, signedIn)
	if id == 0 {
		t.Fatal("session has no user_id after sign-in")
	}
	u, err := s.svc.Get(context.Background(), id)
	if err != nil || u.Email != "olena@example.com" || u.GoogleSub.String != "sub-olena" {
		t.Errorf("signed-in user = %+v, %v", u, err)
	}
	if s.sessionUserID(t, start.cookie) != 0 {
		t.Error("pre-login session token also carries the user")
	}
}

func TestCallbackRejectsForgedMissingAndReplayedState(t *testing.T) {
	s := newAuthSite(t)

	start := s.startLogin(t, "/auth/google/login")
	s.fake.issueCode("code-forged", start.challenge)
	if resp := s.request(t, http.MethodGet, callbackURL("code-forged", "attacker-state"), start.cookie); resp.Code != http.StatusBadRequest {
		t.Errorf("forged state = %d, want 400", resp.Code)
	}

	start = s.startLogin(t, "/auth/google/login")
	s.fake.issueCode("code-nocookie", start.challenge)
	if resp := s.request(t, http.MethodGet, callbackURL("code-nocookie", start.state), nil); resp.Code != http.StatusBadRequest {
		t.Errorf("callback without the login session = %d, want 400 (login CSRF)", resp.Code)
	}

	start = s.startLogin(t, "/auth/google/login")
	s.fake.issueCode("code-ok", start.challenge)
	first := s.request(t, http.MethodGet, callbackURL("code-ok", start.state), start.cookie)
	if first.Code != http.StatusFound {
		t.Fatalf("valid callback = %d, want 302", first.Code)
	}
	s.fake.issueCode("code-replay", start.challenge)
	if resp := s.request(t, http.MethodGet, callbackURL("code-replay", start.state), responseCookie(first)); resp.Code != http.StatusBadRequest {
		t.Errorf("replayed state = %d, want 400: state must be single-use", resp.Code)
	}
}

func TestCallbackFailures(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(f *fakeGoogle)
		query      func(start loginStart) string
		wantStatus int
	}{
		{
			name: "user cancelled on google",
			query: func(st loginStart) string {
				return auth.CallbackPath + "?error=access_denied&state=" + url.QueryEscape(st.state)
			},
			wantStatus: http.StatusFound,
		},
		{
			name:       "google token endpoint down",
			setup:      func(f *fakeGoogle) { f.tokenStatus = http.StatusServiceUnavailable },
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "email not verified by google",
			setup:      func(f *fakeGoogle) { f.profile["email_verified"] = false },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "code issued for another verifier",
			query:      func(st loginStart) string { return callbackURL("code-other", st.state) },
			wantStatus: http.StatusBadGateway,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthSite(t)
			if tc.setup != nil {
				tc.setup(s.fake)
			}
			start := s.startLogin(t, "/auth/google/login")
			s.fake.issueCode("code-1", start.challenge)
			s.fake.issueCode("code-other", oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier()))
			target := callbackURL("code-1", start.state)
			if tc.query != nil {
				target = tc.query(start)
			}
			resp := s.request(t, http.MethodGet, target, start.cookie)
			if resp.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.Code, tc.wantStatus)
			}
			if c := responseCookie(resp); c != nil && s.sessionUserID(t, c) != 0 {
				t.Error("failed sign-in left a user in the session")
			}
			if s.sessionUserID(t, start.cookie) != 0 {
				t.Error("failed sign-in left a user in the session")
			}
		})
	}
}

func TestLogoutDestroysSessionAndOnlyAcceptsPost(t *testing.T) {
	s := newAuthSite(t)
	start := s.startLogin(t, "/auth/google/login")
	s.fake.issueCode("code-1", start.challenge)
	signedIn := responseCookie(s.request(t, http.MethodGet, callbackURL("code-1", start.state), start.cookie))
	if s.sessionUserID(t, signedIn) == 0 {
		t.Fatal("sign-in failed")
	}

	if resp := s.request(t, http.MethodGet, "/auth/logout", signedIn); resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /auth/logout = %d, want 405: a link or <img> must not sign the user out", resp.Code)
	}
	resp := s.request(t, http.MethodPost, "/auth/logout", signedIn)
	if resp.Code != http.StatusSeeOther {
		t.Errorf("POST /auth/logout = %d, want 303", resp.Code)
	}
	if s.sessionUserID(t, signedIn) != 0 {
		t.Error("session still signed in after logout")
	}
}

func TestSignInRoutesAre404WhenGoogleIsNotConfigured(t *testing.T) {
	site := newSite(t, nil)
	for _, p := range []string{"/auth/google/login", auth.CallbackPath + "?code=x&state=y"} {
		if rec := do(t, site, p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
}

func TestSlowGoogleTimesOutWith502(t *testing.T) {
	s := newAuthSiteWithClient(t, &http.Client{Timeout: 200 * time.Millisecond})
	s.fake.mu.Lock()
	s.fake.tokenDelay = 5 * time.Second
	s.fake.mu.Unlock()
	start := s.startLogin(t, "/auth/google/login")
	s.fake.issueCode("code-1", start.challenge)

	began := time.Now()
	rec := s.request(t, http.MethodGet, callbackURL("code-1", start.state), start.cookie)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("slow google = %d, want 502", rec.Code)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Errorf("callback waited %s for google: the http client timeout is not used for the token exchange", took)
	}
}

func TestFailedSignInRetryReturnsToStartPage(t *testing.T) {
	cases := []struct {
		start, wantRetry string
	}{
		{"/en/auth/google/login?next=/org/new?tab=1", `href="/en/auth/google/login?next=%2forg%2fnew%3ftab%3d1"`},
		{"/auth/google/login?next=/org", `href="/auth/google/login?next=%2forg"`},
		{"/auth/google/login", `href="/auth/google/login"`},
	}
	for _, tc := range cases {
		s := newAuthSite(t)
		s.fake.tokenStatus = http.StatusServiceUnavailable
		start := s.startLogin(t, tc.start)
		s.fake.issueCode("code-1", start.challenge)
		body := s.request(t, http.MethodGet, callbackURL("code-1", start.state), start.cookie).Body.String()
		if !strings.Contains(body, tc.wantRetry) {
			t.Errorf("%s: retry link must be %s, so the user lands back where sign-in started", tc.start, tc.wantRetry)
		}
	}
}

func TestSignInFromFailedSignInPageDoesNotReplayCallback(t *testing.T) {
	s := newAuthSite(t)
	s.fake.tokenStatus = http.StatusServiceUnavailable
	start := s.startLogin(t, "/auth/google/login?next=/org")
	s.fake.issueCode("code-used", start.challenge)
	page := s.request(t, http.MethodGet, callbackURL("code-used", start.state), start.cookie).Body.String()
	if strings.Contains(page, "code-used") {
		t.Error("failed sign-in page puts the spent google code into its links")
	}

	m := regexp.MustCompile(`<a class="btn btn-sm btn-tixo" href="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("navbar sign-in link not found on the failed sign-in page")
	}
	s.fake.tokenStatus = 0
	retry := s.startLogin(t, html.UnescapeString(m[1]))
	s.fake.issueCode("code-new", retry.challenge)
	rec := s.request(t, http.MethodGet, callbackURL("code-new", retry.state), retry.cookie)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || strings.HasPrefix(loc, "/auth/") {
		t.Errorf("signing in from the error page navbar ends at %d → %q; want a redirect away from /auth/", rec.Code, loc)
	}
}
