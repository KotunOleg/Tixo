package controllers

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func mustShowEventID(t *testing.T, s *authSite, slug string) int64 {
	t.Helper()
	org := mustOrgID(t, s, slug)
	evs, err := dbgen.New(s.pool).ListEventsForOrganizer(context.Background(), dbgen.ListEventsForOrganizerParams{OrganizerID: org, Lim: 10})
	if err != nil || len(evs) == 0 {
		t.Fatalf("expected an event, got %+v, %v", evs, err)
	}
	return evs[0].ID
}

func tierForm(name, price, quantity string) url.Values {
	return url.Values{"name": {name}, "price": {price}, "quantity": {quantity}}
}

func TestTierCreateAndListOnEventShow(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := mustShowEventID(t, s, "kyiv-live")
	idStr := strconv.FormatInt(id, 10)

	rec := s.postForm(t, "/org/kyiv-live/events/"+idStr+"/tiers", tierForm("Standard", "350.00", "100"), alice)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/org/kyiv-live/events/"+idStr {
		t.Fatalf("create tier = %d → %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}

	show := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+idStr, alice).Body.String()
	if !strings.Contains(show, "Standard") || !strings.Contains(show, "350.00") {
		t.Errorf("event page must show the new tier, got:\n%s", show)
	}
	if !strings.Contains(show, "Категорію квитків додано.") {
		t.Error("event page must show the tier-created flash")
	}
}

func TestTierCreateRejectsForeignEvent(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := mustShowEventID(t, s, "kyiv-live")

	rec := s.request(t, http.MethodGet, "/org/lviv-sound/events/"+strconv.FormatInt(id, 10), bob)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob GET alice's event via lviv-sound = %d, want 404", rec.Code)
	}
}

func TestTierCreateValidatesInput(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)

	cases := []struct {
		form     url.Values
		wantText string
	}{
		{tierForm("", "350.00", "100"), "Вкажіть назву категорії."},
		{tierForm("Standard", "not-a-price", "100"), "Перевірте ціну та кількість."},
		{tierForm("Standard", "350.00", "0"), "Кількість має бути від 1 до 1 000 000"},
	}
	for _, tc := range cases {
		rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers", tc.form, alice)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("form %v: status %d, want 303", tc.form, rec.Code)
			continue
		}
		show := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id, alice).Body.String()
		if !strings.Contains(show, tc.wantText) {
			t.Errorf("form %v: event page does not contain %q, got:\n%s", tc.form, tc.wantText, show)
		}
	}

	org := mustOrgID(t, s, "kyiv-live")
	tiers, err := dbgen.New(s.pool).ListTicketTiers(context.Background(), mustShowEventID(t, s, "kyiv-live"))
	if err != nil || len(tiers) != 0 {
		t.Errorf("rejected submissions must not create tiers, got %+v, %v (org=%d)", tiers, err, org)
	}
}

func TestTierUpdateChangesFields(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)
	s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers", tierForm("Standard", "350.00", "100"), alice)
	org := mustOrgID(t, s, "kyiv-live")
	eventID, _ := strconv.ParseInt(id, 10, 64)
	before, err := dbgen.New(s.pool).ListTicketTiers(context.Background(), eventID)
	if err != nil || len(before) != 1 {
		t.Fatalf("expected one tier, got %+v, %v", before, err)
	}
	tierID := strconv.FormatInt(before[0].ID, 10)

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers/"+tierID, tierForm("VIP", "900.00", "20"), alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update tier = %d\n%s", rec.Code, rec.Body.String())
	}
	after, err := dbgen.New(s.pool).GetTicketTier(context.Background(), dbgen.GetTicketTierParams{ID: before[0].ID, OrganizerID: org})
	if err != nil || after.Name != "VIP" || after.Price != 90000 || after.Quantity != 20 {
		t.Errorf("update result = %+v, %v, want VIP/90000/20", after, err)
	}
}

func TestTierUpdateRejectsForeignTier(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	bob := s.signInAs(t, "sub-bob", "bob@example.com", "Bob")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, bob, "Lviv Sound", "lviv-sound")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)
	s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers", tierForm("Standard", "350.00", "100"), alice)
	eventID, _ := strconv.ParseInt(id, 10, 64)
	before, err := dbgen.New(s.pool).ListTicketTiers(context.Background(), eventID)
	if err != nil || len(before) != 1 {
		t.Fatalf("expected one tier, got %+v, %v", before, err)
	}
	tierID := strconv.FormatInt(before[0].ID, 10)

	rec := s.postForm(t, "/org/lviv-sound/events/"+id+"/tiers/"+tierID, tierForm("Hacked", "1.00", "1"), bob)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob updating alice's tier via lviv-sound = %d, want 404", rec.Code)
	}
}

func TestTierDeleteRemovesUnsoldTier(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)
	s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers", tierForm("Standard", "350.00", "100"), alice)
	eventID, _ := strconv.ParseInt(id, 10, 64)
	before, err := dbgen.New(s.pool).ListTicketTiers(context.Background(), eventID)
	if err != nil || len(before) != 1 {
		t.Fatalf("expected one tier, got %+v, %v", before, err)
	}
	tierID := strconv.FormatInt(before[0].ID, 10)

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers/"+tierID+"/delete", nil, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete tier = %d\n%s", rec.Code, rec.Body.String())
	}
	after, err := dbgen.New(s.pool).ListTicketTiers(context.Background(), eventID)
	if err != nil || len(after) != 0 {
		t.Errorf("tier must be gone after delete, got %+v, %v", after, err)
	}
}

func TestTierDeleteBlockedWhenSold(t *testing.T) {
	s := newAuthSite(t)
	alice := s.signInAs(t, "sub-alice", "alice@example.com", "Alice")
	s.createOrganizer(t, alice, "Kyiv Live", "kyiv-live")
	venueID := mustEventVenue(t, s, "kyiv-live", alice)
	s.postMultipart(t, "/org/kyiv-live/events/new", eventFields(venueID, "big-show"), "", "", nil, alice)
	id := strconv.FormatInt(mustShowEventID(t, s, "kyiv-live"), 10)
	s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers", tierForm("Standard", "350.00", "100"), alice)
	eventID, _ := strconv.ParseInt(id, 10, 64)
	before, err := dbgen.New(s.pool).ListTicketTiers(context.Background(), eventID)
	if err != nil || len(before) != 1 {
		t.Fatalf("expected one tier, got %+v, %v", before, err)
	}
	tierID := before[0].ID
	if _, err := s.pool.Exec(context.Background(), "UPDATE ticket_tiers SET available = available - 1 WHERE id = $1", tierID); err != nil {
		t.Fatal(err)
	}

	rec := s.postForm(t, "/org/kyiv-live/events/"+id+"/tiers/"+strconv.FormatInt(tierID, 10)+"/delete", nil, alice)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete sold tier = %d\n%s", rec.Code, rec.Body.String())
	}
	show := s.request(t, http.MethodGet, "/org/kyiv-live/events/"+id, alice).Body.String()
	if !strings.Contains(show, "Цю категорію вже куплено частково") {
		t.Errorf("event page must show the sold-tier flash, got:\n%s", show)
	}
	after, err := dbgen.New(s.pool).ListTicketTiers(context.Background(), eventID)
	if err != nil || len(after) != 1 {
		t.Errorf("sold tier must survive the delete attempt, got %+v, %v", after, err)
	}
}
