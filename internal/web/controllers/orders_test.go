package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/tiers"
)

var orderPath = regexp.MustCompile(`^/(en/)?orders/[0-9]+$`)

func (s *authSite) tierIDs(t *testing.T, eventID int64) []int64 {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), "SELECT id FROM ticket_tiers WHERE event_id = $1 ORDER BY price, id", eventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (s *authSite) available(t *testing.T, tierID int64) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), "SELECT available FROM ticket_tiers WHERE id = $1", tierID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func buyValues(tierID int64, qty, key string) url.Values {
	return url.Values{"tier_id": {strconv.FormatInt(tierID, 10)}, "quantity": {qty}, "idempotency_key": {key}}
}

func TestBuyWithoutSessionSendsToSignInAndBack(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "anon-buy", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})

	page := s.request(t, http.MethodGet, "/events/anon-buy", nil).Body.String()
	if !strings.Contains(page, "Увійти, щоб купити") || !strings.Contains(page, "next=%2fevents%2fanon-buy") || strings.Contains(page, `name="idempotency_key"`) {
		t.Error("anonymous landing must offer sign-in with return to the event instead of the buy form")
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/en/events/anon-buy/buy", strings.NewReader(buyValues(s.tierIDs(t, id)[0], "1", uuid.NewString()).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/en/auth/google/login?next=%2Fen%2Fevents%2Fanon-buy" {
		t.Errorf("anonymous POST = %d → %q", rec.Code, rec.Header().Get("Location"))
	}
	if n := s.available(t, s.tierIDs(t, id)[0]); n != 5 {
		t.Errorf("available = %d after anonymous POST, want 5", n)
	}
}

func TestBuyFromLandingAndResubmitBuysOnce(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "buy-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})
	tier := s.tierIDs(t, id)[0]
	buyer := s.signInAs(t, "sub-buyer", "buyer@example.com", "Buyer")

	page := s.request(t, http.MethodGet, "/events/buy-show", buyer).Body.String()
	key := regexp.MustCompile(`name="idempotency_key" value="([0-9a-f-]{36})"`).FindStringSubmatch(page)
	if key == nil {
		t.Fatal("signed-in landing has no buy form with an idempotency key")
	}
	if !strings.Contains(page, `value="`+strconv.FormatInt(tier, 10)+`" checked`) {
		t.Error("the only available tier is not preselected")
	}

	first := s.postForm(t, "/events/buy-show/buy", buyValues(tier, "2", key[1]), buyer)
	again := s.postForm(t, "/events/buy-show/buy", buyValues(tier, "2", key[1]), buyer)
	loc := first.Header().Get("Location")
	if first.Code != http.StatusSeeOther || !orderPath.MatchString(loc) {
		t.Fatalf("buy = %d → %q", first.Code, loc)
	}
	if again.Code != http.StatusSeeOther || again.Header().Get("Location") != loc {
		t.Errorf("resubmit = %d → %q, want the same order %q", again.Code, again.Header().Get("Location"), loc)
	}
	if n := s.available(t, tier); n != 3 {
		t.Errorf("available = %d, want 3 (bought once)", n)
	}

	order := s.request(t, http.MethodGet, loc, buyer)
	body := order.Body.String()
	if order.Code != http.StatusOK || !strings.Contains(body, "Show buy-show") || !strings.Contains(body, "600 ₴") {
		t.Errorf("order page = %d, want event title and total 600 ₴", order.Code)
	}
	if codes := regexp.MustCompile(`<code>[0-9a-f-]{36}</code>`).FindAllString(body, -1); len(codes) != 2 {
		t.Errorf("order page shows %d ticket codes, want 2", len(codes))
	}
	if list := s.request(t, http.MethodGet, "/tickets", buyer).Body.String(); !strings.Contains(list, `href="`+loc+`"`) {
		t.Error("my tickets does not link the new order")
	}
}

func TestBuyFormErrorsKeepInput(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "form-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 2}, {Name: "VIP", Price: 90000, Quantity: 2}}})
	other := s.publicEvent(t, publicEvent{slug: "other-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 2}}})
	ids := s.tierIDs(t, id)
	vip := ids[1]
	buyer := s.signInAs(t, "sub-buyer", "buyer@example.com", "Buyer")

	cases := []struct {
		name     string
		form     url.Values
		wantCode int
		want     []string
	}{
		{"quantity out of range", buyValues(vip, "11", uuid.NewString()), http.StatusUnprocessableEntity, []string{"Оберіть від 1 до 10 квитків.", `aria-invalid="true"`, `value="` + strconv.FormatInt(vip, 10) + `" checked`}},
		{"tier of another event", buyValues(s.tierIDs(t, other)[0], "1", uuid.NewString()), http.StatusUnprocessableEntity, []string{"Оберіть категорію квитка зі списку."}},
		{"missing key", buyValues(vip, "1", ""), http.StatusUnprocessableEntity, []string{"Сторінка застаріла."}},
		{"more than left", buyValues(vip, "3", uuid.NewString()), http.StatusConflict, []string{"Стільки квитків цієї категорії вже немає.", `<option value="3" selected>`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := s.postForm(t, "/events/form-show/buy", tc.form, buyer)
			body := rec.Body.String()
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			for _, w := range append([]string{`class="tixo-error-summary`}, tc.want...) {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q", w)
				}
			}
		})
	}
	if s.available(t, ids[0]) != 2 || s.available(t, vip) != 2 {
		t.Error("rejected purchases changed seat counts")
	}
}

func TestOrdersAreOnlyVisibleToBuyer(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "own-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	loc := s.postForm(t, "/events/own-show/buy", buyValues(s.tierIDs(t, id)[0], "1", uuid.NewString()), alice).Header().Get("Location")

	if rec := s.request(t, http.MethodGet, loc, bob); rec.Code != http.StatusNotFound {
		t.Errorf("bob GET alice's order = %d, want 404", rec.Code)
	}
	if list := s.request(t, http.MethodGet, "/tickets", bob).Body.String(); strings.Contains(list, "Show own-show") || !strings.Contains(list, "Квитків поки немає") {
		t.Error("bob's tickets page shows alice's order or lacks the empty state")
	}
	for _, path := range []string{loc, "/tickets"} {
		if rec := s.request(t, http.MethodGet, path, nil); rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/auth/google/login") {
			t.Errorf("anonymous GET %s = %d → %q, want sign-in", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}
