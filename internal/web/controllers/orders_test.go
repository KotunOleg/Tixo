package controllers

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
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

func (s *authSite) finishLoginFrom(t *testing.T, loginURL string) string {
	t.Helper()
	s.fake.mu.Lock()
	s.fake.profile = map[string]any{"sub": "sub-guest", "email": "guest@example.com", "email_verified": true, "name": "Guest"}
	s.fake.mu.Unlock()
	start := s.startLogin(t, loginURL)
	code := "code-" + start.state
	s.fake.issueCode(code, start.challenge)
	rec := s.request(t, http.MethodGet, callbackURL(code, start.state), start.cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback = %d", rec.Code)
	}
	return rec.Header().Get("Location")
}

func TestGuestReturnsToEventAfterSignIn(t *testing.T) {
	signInLink := regexp.MustCompile(`href="([^"]*/auth/google/login\?next=[^"]*)">(Увійти, щоб купити|Sign in to buy)<`)
	for _, tc := range []struct{ prefix, want string }{{"", "/events/guest-show"}, {"/en", "/en/events/guest-show"}} {
		t.Run("lang"+tc.prefix, func(t *testing.T) {
			s := newAuthSite(t)
			id := s.publicEvent(t, publicEvent{slug: "guest-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})

			page := s.request(t, http.MethodGet, tc.prefix+"/events/guest-show", nil).Body.String()
			if strings.Contains(page, `name="idempotency_key"`) {
				t.Error("anonymous landing shows the buy form")
			}
			link := signInLink.FindStringSubmatch(page)
			if link == nil {
				t.Fatal("anonymous landing has no sign-in-to-buy link")
			}
			if got := s.finishLoginFrom(t, html.UnescapeString(link[1])); got != tc.want {
				t.Errorf("sign-in link returns to %q, want %q", got, tc.want)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.prefix+"/events/guest-show/buy", strings.NewReader(buyValues(s.tierIDs(t, id)[0], "1", uuid.NewString()).Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			s.h.ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("anonymous POST = %d", rec.Code)
			}
			if got := s.finishLoginFrom(t, rec.Header().Get("Location")); got != tc.want {
				t.Errorf("anonymous POST returns to %q after sign-in, want %q", got, tc.want)
			}
			if n := s.available(t, s.tierIDs(t, id)[0]); n != 5 {
				t.Errorf("available = %d after anonymous POST, want 5", n)
			}
		})
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

func TestBuyEmitsGA4PurchaseOnceNotOnRevisit(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "ga4-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})
	tier := s.tierIDs(t, id)[0]
	buyer := s.signInAs(t, "sub-ga4", "ga4@example.com", "Buyer")

	bought := s.postForm(t, "/events/ga4-show/buy", buyValues(tier, "2", uuid.NewString()), buyer)
	loc := bought.Header().Get("Location")
	if bought.Code != http.StatusSeeOther || !orderPath.MatchString(loc) {
		t.Fatalf("buy = %d → %q", bought.Code, loc)
	}

	first := s.request(t, http.MethodGet, loc, buyer).Body.String()
	if !strings.Contains(first, `id="ga-purchase">{"transaction_id"`) {
		t.Errorf("first order page has no ga-purchase payload:\n%s", first)
	}
	if !strings.Contains(first, `"value":600`) || !strings.Contains(first, `"currency":"UAH"`) || !strings.Contains(first, `"quantity":2`) {
		t.Errorf("ga-purchase payload missing expected fields:\n%s", first)
	}

	second := s.request(t, http.MethodGet, loc, buyer).Body.String()
	if strings.Contains(second, "ga-purchase") {
		t.Error("second visit to the order page re-sent the ga4 purchase payload: it must fire once")
	}
}

func TestGA4PurchaseIsNotMixedUpBetweenTwoPendingOrders(t *testing.T) {
	s := newAuthSite(t)
	idA := s.publicEvent(t, publicEvent{slug: "ga4-a", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 10000, Quantity: 5}}})
	idB := s.publicEvent(t, publicEvent{slug: "ga4-b", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 20000, Quantity: 5}}})
	buyer := s.signInAs(t, "sub-ga4-ab", "ga4ab@example.com", "Buyer")

	boughtA := s.postForm(t, "/events/ga4-a/buy", buyValues(s.tierIDs(t, idA)[0], "1", uuid.NewString()), buyer)
	locA := boughtA.Header().Get("Location")
	boughtB := s.postForm(t, "/events/ga4-b/buy", buyValues(s.tierIDs(t, idB)[0], "1", uuid.NewString()), buyer)
	locB := boughtB.Header().Get("Location")
	if locA == locB {
		t.Fatalf("both purchases redirected to the same order: %q", locA)
	}

	orderIDFromLocation := regexp.MustCompile(`/orders/([0-9]+)$`)
	idFromA := orderIDFromLocation.FindStringSubmatch(locA)[1]
	idFromB := orderIDFromLocation.FindStringSubmatch(locB)[1]

	pageB := s.request(t, http.MethodGet, locB, buyer).Body.String()
	if !strings.Contains(pageB, `"transaction_id":"`+idFromB+`"`) {
		t.Fatalf("order B page does not carry order B's own transaction_id:\n%s", pageB)
	}
	if strings.Contains(pageB, `"transaction_id":"`+idFromA+`"`) {
		t.Fatalf("order B page carries order A's transaction_id: payload leaked across orders\n%s", pageB)
	}

	pageA := s.request(t, http.MethodGet, locA, buyer).Body.String()
	if !strings.Contains(pageA, `"transaction_id":"`+idFromA+`"`) {
		t.Errorf("order A's ga4 purchase event was lost: viewing order B must not have consumed it\n%s", pageA)
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

func (s *authSite) ticketIDs(t *testing.T, orderID int64) []int64 {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), "SELECT id FROM tickets WHERE order_id = $1 ORDER BY id", orderID)
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

func (s *authSite) markTicketPDFReady(t *testing.T, ticketID int64, content string) string {
	t.Helper()
	store, err := storage.New(s.storageDir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.Save(strings.NewReader(content), "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(context.Background(), "UPDATE tickets SET pdf_key = $1 WHERE id = $2", key, ticketID); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestOrderPageShowsDownloadOnlyOncePDFIsReady(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "pdf-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})
	buyer := s.signInAs(t, "sub-buyer", "buyer@example.com", "Buyer")
	loc := s.postForm(t, "/events/pdf-show/buy", buyValues(s.tierIDs(t, id)[0], "1", uuid.NewString()), buyer).Header().Get("Location")
	orderID := regexp.MustCompile(`/orders/([0-9]+)$`).FindStringSubmatch(loc)[1]
	ticketID := s.ticketIDs(t, mustAtoi(t, orderID))[0]

	before := s.request(t, http.MethodGet, loc, buyer).Body.String()
	if !strings.Contains(before, "PDF генерується") {
		t.Errorf("order page with no pdf_key yet must show the generating badge:\n%s", before)
	}
	if strings.Contains(before, "Завантажити PDF") {
		t.Error("order page offers a download link before pdf_key is set")
	}

	s.markTicketPDFReady(t, ticketID, "ticket pdf bytes")

	after := s.request(t, http.MethodGet, loc, buyer).Body.String()
	wantHref := "/orders/" + orderID + "/tickets/" + strconv.FormatInt(ticketID, 10) + "/pdf"
	if !strings.Contains(after, `href="`+wantHref+`"`) {
		t.Errorf("order page does not link the ready pdf at %q:\n%s", wantHref, after)
	}
	if strings.Contains(after, "PDF генерується") {
		t.Error("order page still shows the generating badge after pdf_key is set")
	}
}

func TestTicketsListShowsPDFStatusBadge(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "badge-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})
	buyer := s.signInAs(t, "sub-buyer", "buyer@example.com", "Buyer")
	loc := s.postForm(t, "/events/badge-show/buy", buyValues(s.tierIDs(t, id)[0], "1", uuid.NewString()), buyer).Header().Get("Location")
	orderID := regexp.MustCompile(`/orders/([0-9]+)$`).FindStringSubmatch(loc)[1]
	ticketID := s.ticketIDs(t, mustAtoi(t, orderID))[0]

	before := s.request(t, http.MethodGet, "/tickets", buyer).Body.String()
	if !strings.Contains(before, "Квитки генеруються") {
		t.Errorf("tickets list must show the generating badge while pdf_key is null:\n%s", before)
	}

	s.markTicketPDFReady(t, ticketID, "ticket pdf bytes")

	after := s.request(t, http.MethodGet, "/tickets", buyer).Body.String()
	if !strings.Contains(after, "Квитки готові") {
		t.Errorf("tickets list must show the ready badge once pdf_key is set:\n%s", after)
	}
}

func TestDownloadPDFStreamsTheFileToItsOwnerOnly(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "download-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	loc := s.postForm(t, "/events/download-show/buy", buyValues(s.tierIDs(t, id)[0], "1", uuid.NewString()), alice).Header().Get("Location")
	orderID := regexp.MustCompile(`/orders/([0-9]+)$`).FindStringSubmatch(loc)[1]
	ticketID := s.ticketIDs(t, mustAtoi(t, orderID))[0]

	pdfURL := "/orders/" + orderID + "/tickets/" + strconv.FormatInt(ticketID, 10) + "/pdf"
	if rec := s.request(t, http.MethodGet, pdfURL, alice); rec.Code != http.StatusNotFound {
		t.Errorf("download before pdf_key is set = %d, want 404", rec.Code)
	}

	s.markTicketPDFReady(t, ticketID, "ticket pdf bytes")

	rec := s.request(t, http.MethodGet, pdfURL, alice)
	if rec.Code != http.StatusOK || rec.Body.String() != "ticket pdf bytes" {
		t.Fatalf("owner download = %d %q, want 200 \"ticket pdf bytes\"", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}

	if rec := s.request(t, http.MethodGet, pdfURL, bob); rec.Code != http.StatusNotFound {
		t.Errorf("bob downloads alice's ticket pdf = %d, want 404", rec.Code)
	}
	if rec := s.request(t, http.MethodGet, pdfURL, nil); rec.Code != http.StatusFound {
		t.Errorf("anonymous download = %d, want 302 to sign-in", rec.Code)
	}
}

func mustAtoi(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
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

func TestLosingTheLastTicketExplainsWhy(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "last-seat", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 1}}})
	tier := s.tierIDs(t, id)[0]
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	if rec := s.postForm(t, "/events/last-seat/buy", buyValues(tier, "1", uuid.NewString()), bob); rec.Code != http.StatusSeeOther {
		t.Fatalf("bob buys the last seat = %d", rec.Code)
	}

	rec := s.postForm(t, "/events/last-seat/buy", buyValues(tier, "1", uuid.NewString()), alice)
	body := rec.Body.String()
	if rec.Code != http.StatusConflict {
		t.Fatalf("alice = %d, want 409", rec.Code)
	}
	for _, want := range []string{`class="tixo-error-summary`, "Стільки квитків цієї категорії вже немає.", "Усе розпродано"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `href="#buy-tiers"`) {
		t.Error("error summary links to a form that is not on the page")
	}
}

func TestTicketsPagePastTheEndGoesToLastPage(t *testing.T) {
	s := newAuthSite(t)
	id := s.publicEvent(t, publicEvent{slug: "paged-show", status: events.StatusPublished, tiers: []tiers.Input{{Name: "GA", Price: 30000, Quantity: 5}}})
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	if rec := s.postForm(t, "/events/paged-show/buy", buyValues(s.tierIDs(t, id)[0], "1", uuid.NewString()), alice); rec.Code != http.StatusSeeOther {
		t.Fatalf("buy = %d", rec.Code)
	}
	for _, tc := range []struct{ path, want string }{
		{"/tickets?page=9", "/tickets"},
		{"/tickets?page=200000000", "/tickets"},
		{"/en/tickets?page=50", "/en/tickets"},
	} {
		rec := s.request(t, http.MethodGet, tc.path, alice)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != tc.want {
			t.Errorf("GET %s = %d → %q, want 302 → %q", tc.path, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
	if rec := s.request(t, http.MethodGet, "/tickets?page=9", s.signInAs(t, "sub-bob", "bob@example.com", "Bob")); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Квитків поки немає") {
		t.Errorf("user without orders on page 9 = %d, want the empty state", rec.Code)
	}
}
