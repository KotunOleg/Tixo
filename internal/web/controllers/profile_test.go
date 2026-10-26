package controllers

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var profileTokenIDPattern = regexp.MustCompile(`/profile/tokens/(\d+)/revoke`)

func (s *authSite) issueToken(t *testing.T, cookie *http.Cookie, name string) int64 {
	t.Helper()
	rec := s.postForm(t, "/profile/tokens", url.Values{"name": {name}}, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue token %q = %d: %s", name, rec.Code, rec.Body.String())
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
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token = %d, want 201\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="`) || strings.Count(body, "laptop") == 0 {
		t.Fatalf("response does not show the issued token: %s", body)
	}

	rec2 := s.request(t, http.MethodGet, "/profile", cookie)
	if strings.Count(rec2.Body.String(), `id="profile-issued-token"`) != 0 {
		t.Fatalf("raw token is shown again on a plain GET /profile")
	}
	if !strings.Contains(rec2.Body.String(), "laptop") {
		t.Fatalf("token name missing from the list: %s", rec2.Body.String())
	}
}

func TestIssueTokenRejectsBlankName(t *testing.T) {
	s := newAuthSite(t)
	cookie := s.signInAs(t, "sub-owner", "owner2@example.com", "Owner")

	rec := s.postForm(t, "/profile/tokens", url.Values{"name": {"  "}}, cookie)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create blank-name token = %d, want 422\n%s", rec.Code, rec.Body.String())
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
