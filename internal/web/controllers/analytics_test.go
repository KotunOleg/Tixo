package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/features"
	"github.com/KotunOleg/Tixo/internal/tiers"
)

func (s *authSite) setPlan(t *testing.T, slug, plan string) int64 {
	t.Helper()
	q := dbgen.New(s.pool)
	org, err := q.GetOrganizerBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetOrganizerPlan(context.Background(), dbgen.SetOrganizerPlanParams{ID: org.ID, Plan: plan}); err != nil {
		t.Fatal(err)
	}
	return org.ID
}

func (s *authSite) sellTickets(t *testing.T, slug string, buyer int64, qty int) {
	t.Helper()
	ctx := context.Background()
	var orgID, eventID int64
	if err := s.pool.QueryRow(ctx, "SELECT o.id, e.id FROM events e JOIN organizers o ON o.id = e.organizer_id WHERE o.slug = $1 ORDER BY e.id LIMIT 1", slug).Scan(&orgID, &eventID); err != nil {
		t.Fatal(err)
	}
	tier, err := tiers.New(s.pool).Create(ctx, orgID, eventID, tiers.Input{Name: "Standard", Price: 120000, Quantity: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO orders (user_id, event_id, tier_id, quantity, unit_price, total, idempotency_key)
		VALUES ($1, $2, $3, $4, 120000, $4 * 120000, gen_random_uuid())`, buyer, eventID, tier.ID, qty); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyticsPageAccessMatrix(t *testing.T) {
	s := newAuthSite(t)
	premium := s.signInAs(t, "sub-premium", "premium@example.com", "Premium Owner")
	free := s.signInAs(t, "sub-free", "free@example.com", "Free Owner")
	override := s.signInAs(t, "sub-override", "override@example.com", "Override Owner")
	stranger := s.signInAs(t, "sub-stranger", "stranger@example.com", "Stranger")
	s.createOrganizer(t, premium, "Kyiv Live", "kyiv-live")
	s.createOrganizer(t, free, "Lviv Sound", "lviv-sound")
	s.createOrganizer(t, override, "Odesa Jazz", "odesa-jazz")
	s.createOrganizer(t, stranger, "Stranger Org", "stranger-org")
	s.setPlan(t, "kyiv-live", "premium")
	overrideID := s.setPlan(t, "odesa-jazz", "free")
	if err := s.deps.Features.Enable(context.Background(), overrideID, features.SalesAnalytics); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		path     string
		cookie   *http.Cookie
		wantCode int
		wantText string
	}{
		{"guest is sent to sign-in", "/org/kyiv-live/analytics", nil, http.StatusFound, ""},
		{"member of another organizer gets 404", "/org/kyiv-live/analytics", stranger, http.StatusNotFound, ""},
		{"free plan gets the premium explanation", "/org/lviv-sound/analytics", free, http.StatusForbidden, "Аналітика доступна в Premium"},
		{"free plan explanation in english", "/en/org/lviv-sound/analytics", free, http.StatusForbidden, "Analytics is a Premium feature"},
		{"free plan with flag override sees analytics", "/org/odesa-jazz/analytics", override, http.StatusOK, "Аналітика продажів"},
		{"premium sees analytics", "/org/kyiv-live/analytics", premium, http.StatusOK, "Аналітика продажів"},
		{"premium sees analytics in english", "/en/org/kyiv-live/analytics", premium, http.StatusOK, "Sales analytics"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := s.request(t, http.MethodGet, tc.path, tc.cookie)
			if rec.Code != tc.wantCode {
				t.Fatalf("GET %s = %d, want %d", tc.path, rec.Code, tc.wantCode)
			}
			if tc.wantCode == http.StatusFound && !strings.HasPrefix(rec.Header().Get("Location"), "/auth/google/login?next=") {
				t.Errorf("redirect = %q, want sign-in", rec.Header().Get("Location"))
			}
			if tc.wantText != "" && !strings.Contains(rec.Body.String(), tc.wantText) {
				t.Errorf("body lacks %q", tc.wantText)
			}
			if tc.wantCode == http.StatusNotFound && strings.Contains(rec.Body.String(), "Kyiv Live") {
				t.Error("404 page leaks the organizer name")
			}
		})
	}
}

func TestAnalyticsPageShowsSalesAndEscapesEventTitles(t *testing.T) {
	s := newAuthSite(t)
	owner := s.signInAs(t, "sub-premium", "premium@example.com", "Premium Owner")
	s.createOrganizer(t, owner, "Kyiv Live", "kyiv-live")
	s.setPlan(t, "kyiv-live", "premium")
	hostile := `</script><script>alert("x")</script>`
	s.addEvent(t, "kyiv-live", hostile)

	empty := s.request(t, http.MethodGet, "/org/kyiv-live/analytics", owner).Body.String()
	if !strings.Contains(empty, "Ще немає продажів") || strings.Contains(empty, "Продажі за днями") {
		t.Error("organizer without sales must see the empty state instead of sales tables")
	}

	s.sellTickets(t, "kyiv-live", s.sessionUserID(t, owner), 3)
	rec := s.request(t, http.MethodGet, "/org/kyiv-live/analytics", owner)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{"Продажі за днями", "Продажі за подіями", "3\u00a0600\u00a0₴", "Рок"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `<script>alert("x")`) {
		t.Error("event title reached the page unescaped")
	}
}

func TestOrganizerPageLinksToAnalyticsForEveryPlan(t *testing.T) {
	s := newAuthSite(t)
	free := s.signInAs(t, "sub-free", "free@example.com", "Free Owner")
	s.createOrganizer(t, free, "Lviv Sound", "lviv-sound")
	body := s.request(t, http.MethodGet, "/org/lviv-sound", free).Body.String()
	if !strings.Contains(body, `href="/org/lviv-sound/analytics"`) {
		t.Error("free organizer page must link to analytics so the premium explanation is discoverable")
	}
}

var chartDataBlock = regexp.MustCompile(`(?s)<script type="application/json" id="analytics-data">(.*?)</script>`)

func TestAnalyticsChartDataIsLocalizedJSONThatCannotBreakOutOfItsScriptTag(t *testing.T) {
	s := newAuthSite(t)
	owner := s.signInAs(t, "sub-premium", "premium@example.com", "Premium Owner")
	s.createOrganizer(t, owner, "Kyiv Live", "kyiv-live")
	s.setPlan(t, "kyiv-live", "premium")
	hostile := `</script><script>alert("x")</script>`
	s.addEvent(t, "kyiv-live", hostile)
	s.sellTickets(t, "kyiv-live", s.sessionUserID(t, owner), 3)

	cases := []struct {
		path, wantGenre, wantTickets string
	}{
		{"/org/kyiv-live/analytics", "Рок", "Квитки"},
		{"/en/org/kyiv-live/analytics", "Rock", "Tickets"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			body := s.request(t, http.MethodGet, tc.path, owner).Body.String()
			blocks := chartDataBlock.FindAllStringSubmatch(body, -1)
			if len(blocks) != 1 {
				t.Fatalf("found %d chart data blocks, want exactly 1 (a raw </script> in data would split it)", len(blocks))
			}
			var got analyticsChart
			if err := json.Unmarshal([]byte(blocks[0][1]), &got); err != nil {
				t.Fatalf("chart data is not JSON: %v\n%s", err, blocks[0][1])
			}
			if len(got.Days) != 30 || got.Days[29].Tickets != 3 || got.Days[29].Revenue != 360000 {
				t.Errorf("days = %d points, last %+v; want 30 with today's 3 tickets", len(got.Days), got.Days[len(got.Days)-1])
			}
			if len(got.EventsTickets) != 1 || got.EventsTickets[0] != (chartBar{Label: hostile, Value: 3}) {
				t.Errorf("events = %+v, want the title intact as data", got.EventsTickets)
			}
			if len(got.Genres) != 1 || got.Genres[0].Label != tc.wantGenre || got.TicketsLabel != tc.wantTickets {
				t.Errorf("labels = %+v / %q, want %q / %q", got.Genres, got.TicketsLabel, tc.wantGenre, tc.wantTickets)
			}
		})
	}
}
