package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

func (s testSite) buy(t *testing.T, bearer, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if key != "" {
		req.Header.Set(idempotencyHeader, key)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func (s testSite) eventOnSale(t *testing.T, orgSlug, eventSlug string, seats int32, publish bool) int64 {
	t.Helper()
	ctx := t.Context()
	owner := s.createUser(t, orgSlug+"@example.com")
	org := s.createOrganizer(t, owner, orgSlug, orgSlug)
	v, err := s.venues.Create(ctx, org.ID, venues.Input{Name: "Hall", Address: "1 Street", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	ev, err := s.events.Create(ctx, org.ID, events.Input{VenueID: v.ID, Slug: eventSlug, Title: "Show", StartsAt: start, EndsAt: start.Add(time.Hour), Genre: "rock"})
	if err != nil {
		t.Fatal(err)
	}
	tier, err := s.tiers.Create(ctx, org.ID, ev.ID, tiers.Input{Name: "Standard", Price: 50000, Quantity: seats})
	if err != nil {
		t.Fatal(err)
	}
	if publish {
		if _, err := s.events.SetStatus(ctx, org.ID, ev.ID, events.StatusPublished); err != nil {
			t.Fatal(err)
		}
	}
	return tier.ID
}

func decodeOrder(t *testing.T, rec *httptest.ResponseRecorder) orderJSON {
	t.Helper()
	var o orderJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &o); err != nil {
		t.Fatalf("decode order: %v: %s", err, rec.Body.String())
	}
	return o
}

func TestCreateOrderStatuses(t *testing.T) {
	s := newTestSite(t)
	tier := s.eventOnSale(t, "org-on-sale", "on-sale", 3, true)
	draftTier := s.eventOnSale(t, "org-draft", "draft-show", 3, false)
	token := s.tokenFor(t, s.createUser(t, "buyer@example.com"))
	body := func(tierID int64, qty int) string { return fmt.Sprintf(`{"tierId":%d,"quantity":%d}`, tierID, qty) }

	cases := []struct {
		name      string
		bearer    string
		key       string
		body      string
		wantCode  int
		wantError string
	}{
		{"no idempotency key", token, "", body(tier, 1), http.StatusBadRequest, ""},
		{"key is not a uuid", token, "retry-1", body(tier, 1), http.StatusBadRequest, ""},
		{"not signed in", "", uuid.NewString(), body(tier, 1), http.StatusUnauthorized, ""},
		{"malformed json", token, uuid.NewString(), `{"tierId":`, http.StatusBadRequest, ""},
		{"unknown tier", token, uuid.NewString(), body(999999, 1), http.StatusNotFound, ""},
		{"quantity above limit", token, uuid.NewString(), body(tier, 11), http.StatusUnprocessableEntity, "validation failed"},
		{"draft event is hidden", token, uuid.NewString(), body(draftTier, 1), http.StatusNotFound, ""},
		{"more than seats left", token, uuid.NewString(), body(tier, 4), http.StatusConflict, "sold_out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := s.buy(t, tc.bearer, tc.key, tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantError != "" && !strings.Contains(rec.Body.String(), `"error":"`+tc.wantError+`"`) {
				t.Errorf("body = %s, want error %q", rec.Body.String(), tc.wantError)
			}
		})
	}
	var left int32
	if err := s.pool.QueryRow(t.Context(), "SELECT available FROM ticket_tiers WHERE id = $1", tier).Scan(&left); err != nil || left != 3 {
		t.Errorf("available after rejected requests = %d (%v), want 3", left, err)
	}
}

func TestCreateOrderReplaysSameKey(t *testing.T) {
	s := newTestSite(t)
	tier := s.eventOnSale(t, "org-replay", "replay-show", 5, true)
	token := s.tokenFor(t, s.createUser(t, "buyer@example.com"))
	key := uuid.NewString()
	body := fmt.Sprintf(`{"tierId":%d,"quantity":2}`, tier)

	first := s.buy(t, token, key, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first = %d: %s", first.Code, first.Body.String())
	}
	created := decodeOrder(t, first)
	if created.Total != 100000 || len(created.Tickets) != 2 || created.Tickets[0].Code == created.Tickets[1].Code {
		t.Errorf("created = %+v, want total 100000 and 2 distinct ticket codes", created)
	}

	again := s.buy(t, token, key, body)
	if again.Code != http.StatusOK || decodeOrder(t, again).ID != created.ID {
		t.Errorf("retry = %d %s, want 200 with order %d", again.Code, again.Body.String(), created.ID)
	}

	other := s.buy(t, token, key, fmt.Sprintf(`{"tierId":%d,"quantity":1}`, tier))
	if other.Code != http.StatusUnprocessableEntity || !strings.Contains(other.Body.String(), "idempotency_key_reused") {
		t.Errorf("same key, other body = %d %s, want 422 idempotency_key_reused", other.Code, other.Body.String())
	}

	var left int32
	if err := s.pool.QueryRow(t.Context(), "SELECT available FROM ticket_tiers WHERE id = $1", tier).Scan(&left); err != nil || left != 3 {
		t.Errorf("available = %d (%v), want 3 (seats taken once)", left, err)
	}
}

func TestOrdersAreScopedToTokenOwner(t *testing.T) {
	s := newTestSite(t)
	tier := s.eventOnSale(t, "org-scope", "scope-show", 5, true)
	alice := s.tokenFor(t, s.createUser(t, "alice@example.com"))
	bob := s.tokenFor(t, s.createUser(t, "bob@example.com"))
	created := decodeOrder(t, s.buy(t, alice, uuid.NewString(), fmt.Sprintf(`{"tierId":%d,"quantity":1}`, tier)))

	if rec := s.request(t, http.MethodGet, fmt.Sprintf("/api/v1/orders/%d", created.ID), bob, ""); rec.Code != http.StatusNotFound {
		t.Errorf("bob GET alice's order = %d, want 404", rec.Code)
	}
	own := s.request(t, http.MethodGet, fmt.Sprintf("/api/v1/orders/%d", created.ID), alice, "")
	if own.Code != http.StatusOK || len(decodeOrder(t, own).Tickets) != 1 {
		t.Errorf("alice GET own order = %d %s, want 200 with 1 ticket", own.Code, own.Body.String())
	}

	for name, tc := range map[string]struct {
		token string
		count int64
	}{"alice": {alice, 1}, "bob": {bob, 0}} {
		rec := s.request(t, http.MethodGet, "/api/v1/orders", tc.token, "")
		var page struct {
			Items []orderJSON `json:"items"`
			Count int64       `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s list = %d %s", name, rec.Code, rec.Body.String())
		}
		if page.Count != tc.count || int64(len(page.Items)) != tc.count {
			t.Errorf("%s list: count %d, items %d; want %d", name, page.Count, len(page.Items), tc.count)
		}
	}
}
