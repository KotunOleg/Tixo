package controllers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/notify"
)

var profileTokenIDPattern = regexp.MustCompile(`/profile/tokens/(\d+)/revoke`)

func (s *authSite) issueToken(t *testing.T, cookie *http.Cookie, name string) int64 {
	t.Helper()
	rec := s.postForm(t, "/profile/tokens", url.Values{"name": {name}}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("issue token %q = %d → %q, want 303 to /profile", name, rec.Code, rec.Header().Get("Location"))
	}
	list := s.request(t, http.MethodGet, "/profile", cookie)
	m := profileTokenIDPattern.FindStringSubmatch(list.Body.String())
	if m == nil {
		t.Fatalf("could not find a token id on the profile page: %s", list.Body.String())
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestProfilePageRequiresSignIn(t *testing.T) {
	s := newAuthSite(t)
	rec := s.request(t, http.MethodGet, "/profile", nil)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/google/login?next=") {
		t.Errorf("anonymous GET /profile = %d → %q, want 302 to sign-in", rec.Code, rec.Header().Get("Location"))
	}
}

func TestIssueTokenShowsRawValueOnce(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signInAs(t, "sub-owner", "owner@example.com", "Owner")

	rec := s.postForm(t, "/profile/tokens", url.Values{"name": {"laptop"}}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("create token = %d → %q, want 303 to /profile", rec.Code, rec.Header().Get("Location"))
	}

	rec1 := s.request(t, http.MethodGet, "/profile", cookie)
	body := rec1.Body.String()
	if !strings.Contains(body, `id="profile-issued-token"`) || strings.Count(body, "laptop") == 0 {
		t.Fatalf("profile page after the redirect does not show the issued token: %s", body)
	}

	rec2 := s.request(t, http.MethodGet, "/profile", cookie)
	if strings.Count(rec2.Body.String(), `id="profile-issued-token"`) != 0 {
		t.Fatalf("raw token is shown again on a second GET /profile")
	}
	if !strings.Contains(rec2.Body.String(), "laptop") {
		t.Fatalf("token name missing from the list: %s", rec2.Body.String())
	}
}

func TestCreateTokenRedirectsInsteadOfRenderingOnTheTokensPath(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signInAs(t, "sub-owner-poll", "owner-poll@example.com", "Owner")

	rec := s.postForm(t, "/profile/tokens", url.Values{"name": {"laptop"}}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("create token = %d → %q, want 303 to /profile", rec.Code, rec.Header().Get("Location"))
	}

	rec = s.postForm(t, "/profile/tokens", url.Values{"name": {"  "}}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("create blank-name token = %d → %q, want 303 to /profile", rec.Code, rec.Header().Get("Location"))
	}

	if rec := s.request(t, http.MethodGet, "/en/profile", cookie); rec.Code != http.StatusOK {
		t.Fatalf("switching language after creating a token: GET /en/profile = %d, want 200 (browser must be left on a GET-able path)", rec.Code)
	}
}

func TestIssueTokenRejectsBlankName(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signInAs(t, "sub-owner", "owner2@example.com", "Owner")

	rec := s.postForm(t, "/profile/tokens", url.Values{"name": {"  "}}, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("create blank-name token = %d → %q, want 303 to /profile", rec.Code, rec.Header().Get("Location"))
	}
	body := s.request(t, http.MethodGet, "/profile", cookie).Body.String()
	if !strings.Contains(body, `id="profile-token-errors"`) {
		t.Fatalf("profile page after the redirect does not show the validation error: %s", body)
	}
}

func TestRevokeTokenRemovesItFromTheList(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signInAs(t, "sub-owner", "owner3@example.com", "Owner")
	tokenID := s.issueToken(t, cookie, "ci")

	rec := s.postForm(t, "/profile/tokens/"+strconv.FormatInt(tokenID, 10)+"/revoke", nil, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("revoke = %d → %q, want 303 to /profile", rec.Code, rec.Header().Get("Location"))
	}

	rec2 := s.request(t, http.MethodGet, "/profile", cookie)
	if strings.Contains(rec2.Body.String(), "ci<") {
		t.Fatalf("revoked token still listed: %s", rec2.Body.String())
	}
}

func TestRevokeTokenRejectsForeignOwner(t *testing.T) {
	s := newAuthSite(t)
	owner := s.signInAs(t, "sub-owner", "owner4@example.com", "Owner")
	stranger := s.signInAs(t, "sub-stranger", "stranger4@example.com", "Stranger")
	tokenID := s.issueToken(t, owner, "laptop")

	rec := s.postForm(t, "/profile/tokens/"+strconv.FormatInt(tokenID, 10)+"/revoke", nil, stranger)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign revoke = %d, want 404", rec.Code)
	}
}

var telegramStartPattern = regexp.MustCompile(`^https://t\.me/tixo_test_bot\?start=([A-Za-z0-9_-]+)$`)

func (s *authSite) connectTelegram(t *testing.T, cookie *http.Cookie, chatID int64) {
	t.Helper()
	rec := s.postForm(t, "/profile/telegram/link", nil, cookie)
	m := telegramStartPattern.FindStringSubmatch(rec.Header().Get("Location"))
	if rec.Code != http.StatusSeeOther || m == nil {
		t.Fatalf("link = %d → %q, want 303 to the bot with a start token", rec.Code, rec.Header().Get("Location"))
	}
	if err := s.deps.Telegram.HandleUpdate(t.Context(), notify.Update{Message: &notify.Message{
		Chat: notify.Chat{ID: chatID, Type: "private"}, From: &notify.User{LanguageCode: "uk"}, Text: "/start " + m[1],
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestProfileTelegramSectionFollowsTheLinkLifecycle(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signInAs(t, "sub-tg", "tg@example.com", "Telegram User")
	page := func(path string) string {
		t.Helper()
		rec := s.request(t, http.MethodGet, path, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		return rec.Body.String()
	}

	if body := page("/profile"); !strings.Contains(body, "Підключити Telegram") || !strings.Contains(body, `action="/profile/telegram/link" target="_blank"`) || !strings.Contains(body, `data-telegram-state="none"`) {
		t.Fatalf("not connected: want the connect button opening t.me in a new tab so the profile stays open\n%s", body)
	}

	rec := s.postForm(t, "/en/profile/telegram/link", nil, cookie)
	if rec.Code != http.StatusSeeOther || !telegramStartPattern.MatchString(rec.Header().Get("Location")) {
		t.Fatalf("link = %d → %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := page("/en/profile"); !strings.Contains(body, "Open Telegram again") || !strings.Contains(body, "The link works until") || !strings.Contains(body, `data-telegram-state="pending"`) || !strings.Contains(body, `data-status-url="/en/profile/telegram/status"`) {
		t.Fatalf("pending: want the reopen button and expiry\n%s", body)
	}

	s.connectTelegram(t, cookie, 4242)
	body := page("/profile")
	if !strings.Contains(body, "Підключено") || !strings.Contains(body, `action="/profile/telegram/unlink"`) || strings.Contains(body, "Підключити Telegram") || !strings.Contains(body, `data-telegram-state="linked"`) {
		t.Fatalf("connected: want status and the turn-off button only\n%s", body)
	}

	rec = s.postForm(t, "/profile/telegram/unlink", nil, cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile" {
		t.Fatalf("unlink = %d → %q, want 303 to /profile", rec.Code, rec.Header().Get("Location"))
	}
	if body := page("/profile"); !strings.Contains(body, "Нагадування в Telegram вимкнено.") || !strings.Contains(body, "Підключити Telegram") {
		t.Fatalf("after unlink: want the flash and the connect button again\n%s", body)
	}
}

func TestProfileTelegramIsOnlyForSignedInUsers(t *testing.T) {
	s := newAuthSite(t)
	cases := []struct{ method, path string }{
		{http.MethodPost, "/profile/telegram/link"},
		{http.MethodPost, "/profile/telegram/unlink"},
		{http.MethodGet, "/profile/telegram/status"},
	}
	for _, tc := range cases {
		rec := s.request(t, tc.method, tc.path, nil)
		if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/google/login") {
			t.Errorf("anonymous %s %s = %d → %q, want redirect to sign-in", tc.method, tc.path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestProfileHidesTelegramWhenTheBotIsNotConfigured(t *testing.T) {
	s := newAuthSiteTweaked(t, nil, nil, func(d *Deps) { d.Telegram = nil })
	cookie := s.signInAs(t, "sub-no-tg", "notg@example.com", "No Telegram")

	if body := s.request(t, http.MethodGet, "/profile", cookie).Body.String(); strings.Contains(body, "profile-telegram-title") {
		t.Errorf("profile shows the telegram section without a bot")
	}
	if rec := s.postForm(t, "/profile/telegram/link", nil, cookie); rec.Code != http.StatusNotFound {
		t.Errorf("link without a bot = %d, want 404", rec.Code)
	}
	if rec := s.request(t, http.MethodGet, "/profile/telegram/status", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("status without a bot = %d, want 404", rec.Code)
	}
}

func (s *authSite) telegramState(t *testing.T, cookie *http.Cookie) string {
	t.Helper()
	return s.telegramStatus(t, cookie).State
}

type telegramStatusBody struct {
	State   string `json:"state"`
	Expires string `json:"expires"`
}

func (s *authSite) telegramStatus(t *testing.T, cookie *http.Cookie) telegramStatusBody {
	t.Helper()
	rec := s.request(t, http.MethodGet, "/profile/telegram/status", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store so a proxy never serves a stale or foreign state", cc)
	}
	var body telegramStatusBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("status body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestTelegramStatusReportsOnlyTheSignedInUsersLink(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-tg-alice", "alice-tg@example.com", "Alice")
	bob := s.signInAs(t, "sub-tg-bob", "bob-tg@example.com", "Bob")

	if got := s.telegramState(t, alice); got != "none" {
		t.Errorf("fresh user state = %q, want none", got)
	}
	if rec := s.postForm(t, "/profile/telegram/link", nil, alice); rec.Code != http.StatusSeeOther {
		t.Fatalf("link = %d", rec.Code)
	}
	if got := s.telegramState(t, alice); got != "pending" {
		t.Errorf("after issuing a link state = %q, want pending", got)
	}
	s.connectTelegram(t, bob, 77)
	if got := s.telegramState(t, alice); got != "pending" {
		t.Errorf("alice state = %q after bob linked, want her own pending", got)
	}
	if got := s.telegramState(t, bob); got != "linked" {
		t.Errorf("bob state = %q, want linked", got)
	}
}

func TestTelegramStatusChangesWhenTheLinkIsIssuedAgain(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signInAs(t, "sub-tg-reopen", "reopen-tg@example.com", "Reopen")
	issue := func() telegramStatusBody {
		t.Helper()
		if rec := s.postForm(t, "/profile/telegram/link", nil, cookie); rec.Code != http.StatusSeeOther {
			t.Fatalf("link = %d", rec.Code)
		}
		st := s.telegramStatus(t, cookie)
		rec := s.request(t, http.MethodGet, "/profile", cookie)
		if want := `data-telegram-expires="` + st.Expires + `"`; st.Expires == "" || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("status %+v, page must render the same expiry the status reports (%s)", st, want)
		}
		return st
	}

	first := issue()
	second := issue()
	if second.State != "pending" || second.Expires == first.Expires {
		t.Errorf("after reopening status = %+v, first = %+v; the expiry must change so the open profile refreshes the shown time", second, first)
	}
	s.connectTelegram(t, cookie, 4343)
	if st := s.telegramStatus(t, cookie); st.State != "linked" || st.Expires != "" {
		t.Errorf("linked status = %+v, want no expiry", st)
	}
}
