//go:build ui

package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

type uiServer struct {
	Base       string            `json:"base"`
	NoAuth     string            `json:"no_auth"`
	CookieName string            `json:"cookie_name"`
	Cookies    map[string]string `json:"cookies"`
}

var uiAuthErrors = []string{msgAuthState, msgAuthProvider, msgAuthUnverified, msgAuthTaken}

func TestUISnapshotServer(t *testing.T) {
	out := os.Getenv("UI_OUT")
	if out == "" {
		t.Skip("UI_OUT not set: this test only serves demo pages for the screenshot harness")
	}
	var site *authSite
	site = newAuthSiteWith(t, nil, func(r chi.Router) {
		r.Get("/ui/auth-error/{kind}", func(w http.ResponseWriter, r *http.Request) {
			id := "auth.error." + chi.URLParam(r, "kind")
			if !slices.Contains(uiAuthErrors, id) {
				site.deps.View.NotFound(w, r)
				return
			}
			site.deps.View.Render(w, r, http.StatusBadRequest, "pages/auth_error", authErrorData{MessageID: id})
		})
		r.Get("/ui/boom", func(http.ResponseWriter, *http.Request) { panic("ui snapshot: server error page") })
	})

	owner := site.signInAs(t, "sub-owner", "oleksandra.kostiantynopolska-shevchenko@example.com", "Олександра-Вікторія Костянтинопольська-Шевченко")
	member := site.signInAs(t, "sub-member", "bob@example.com", "Bob")
	newcomer := site.signInAs(t, "sub-newcomer", "carol@example.com", "Carol")

	site.createOrganizer(t, owner, "Київська міська філармонія імені Миколи Лисенка та асоціація камерної музики", "kyiv-philharmonic")
	site.createOrganizer(t, owner, "Lviv Sound", "lviv-sound")
	site.createOrganizer(t, owner, "Odesa Jazz Fest", "odesa-jazz")
	site.seedEvents(t, "kyiv-philharmonic", []uiEvent{
		{"Симфонічний вечір: Лятошинський, Сильвестров і прем'єра твору молодих композиторів", "published", 3},
		{"Камерний концерт", "draft", 10},
		{"Органна ніч", "cancelled", 20},
	})
	site.seedLanding(t)
	if rec := site.postForm(t, "/org/kyiv-philharmonic/members", url.Values{"email": {"bob@example.com"}}, owner); rec.Code != http.StatusSeeOther {
		t.Fatalf("add member = %d", rec.Code)
	}

	memberID := site.sessionUserID(t, member)
	site.seedOrders(t, memberID)
	for _, name := range []string{"ноутбук", "скрипт резервного копіювання квитків і статистики продажів на дроплеті"} {
		if _, _, err := site.deps.APITokens.Issue(context.Background(), memberID, name); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(site.h)
	t.Cleanup(srv.Close)
	noAuth := httptest.NewServer(newSite(t, nil))
	t.Cleanup(noAuth.Close)

	info := uiServer{
		Base:       srv.URL,
		NoAuth:     noAuth.URL,
		CookieName: owner.Name,
		Cookies: map[string]string{
			"owner":    owner.Value,
			"member":   member.Value,
			"newcomer": newcomer.Value,
		},
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("ui snapshot server at %s (sign-in disabled: %s)", srv.URL, noAuth.URL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(30 * time.Minute):
	}
}

type uiEvent struct {
	title  string
	status string
	inDays int
}

func (s *authSite) seedEvents(t *testing.T, slug string, events []uiEvent) {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(s.pool)
	org, err := q.GetOrganizerBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	v, err := q.CreateVenue(ctx, dbgen.CreateVenueParams{
		OrganizerID: org.ID, Name: "Національна філармонія", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		start := time.Date(2030, 3, 1, 19, 0, 0, 0, time.UTC).AddDate(0, 0, e.inDays)
		ev, err := q.CreateEvent(ctx, dbgen.CreateEventParams{
			OrganizerID: org.ID, VenueID: v.ID, Slug: slug + "-event-" + string(rune('a'+i)), Title: e.title,
			StartsAt: start, EndsAt: start.Add(2 * time.Hour), Genre: "classical",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.SetEventStatus(ctx, dbgen.SetEventStatusParams{Status: e.status, ID: ev.ID, OrganizerID: org.ID}); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *authSite) seedLanding(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(s.pool)
	org, err := q.GetOrganizerBySlug(ctx, "kyiv-philharmonic")
	if err != nil {
		t.Fatal(err)
	}
	contacts := json.RawMessage(`{"email":"kasa@filarmonia.example","phone":"+380 44 278 16 97","telegram":"@kyiv_philharmonic"}`)
	if _, err := q.UpdateOrganizer(ctx, dbgen.UpdateOrganizerParams{ID: org.ID, Name: org.Name, Contacts: contacts}); err != nil {
		t.Fatal(err)
	}
	ev, err := q.GetPublicEventBySlug(ctx, "kyiv-philharmonic-event-a")
	if err != nil {
		t.Fatal(err)
	}
	description := "Вечір української симфонічної музики XX–XXI століть.\n\nУ програмі — Третя симфонія Бориса Лятошинського, «Тихі пісні» Валентина Сильвестрова в оркестровій версії та прем'єра твору переможця конкурсу молодих композиторів. Диригує запрошений маестро; перед концертом — коротка розмова з музикознавицею у фоє.\n\nТривалість — близько двох годин з антрактом."
	if _, err := s.pool.Exec(ctx, "UPDATE events SET description = $1 WHERE id = $2", description, ev.Event.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE venues SET address = $1 WHERE id = $2", "вул. Володимирський узвіз, 2", ev.Venue.ID); err != nil {
		t.Fatal(err)
	}
	for i, tier := range []struct {
		name     string
		price    int64
		quantity int32
		sold     int32
	}{
		{"Партер, ряди 1–10", 120000, 200, 199},
		{"Амфітеатр", 65000, 300, 120},
		{"Балкон — обмежений огляд сцени через колони історичної зали", 35000, 80, 80},
	} {
		created, err := q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{EventID: ev.Event.ID, OrganizerID: org.ID, Name: tier.name, Price: tier.price, Quantity: tier.quantity})
		if err != nil {
			t.Fatalf("tier %d: %v", i, err)
		}
		if _, err := s.pool.Exec(ctx, "UPDATE ticket_tiers SET available = available - $1 WHERE id = $2", tier.sold, created.ID); err != nil {
			t.Fatal(err)
		}
	}
	for i, name := range []string{"Національний симфонічний оркестр України", "Kyiv Camerata", "Анна Ошмарина-Ковальчук-Сидоренко (сопрано)"} {
		a, err := q.CreateArtist(ctx, dbgen.CreateArtistParams{Name: name, SortName: name})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.SetEventArtist(ctx, dbgen.SetEventArtistParams{EventID: ev.Event.ID, OrganizerID: org.ID, ArtistID: a.ID, Position: int32(i), IsHeadliner: i == 0}); err != nil {
			t.Fatal(err)
		}
	}

	lviv, err := q.GetOrganizerBySlug(ctx, "lviv-sound")
	if err != nil {
		t.Fatal(err)
	}
	v, err := q.CreateVenue(ctx, dbgen.CreateVenueParams{OrganizerID: lviv.ID, Name: "Malevich", City: "Lviv", Lat: 49.84, Lng: 24.03, Capacity: 500})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 5, 10, 20, 0, 0, 0, time.UTC)
	for _, e := range []struct {
		slug    string
		title   string
		soldOut bool
	}{
		{"lviv-bare", "Lviv Indie Night", false},
		{"lviv-sold-out", "Sold Out Gig", true},
	} {
		created, err := q.CreateEvent(ctx, dbgen.CreateEventParams{OrganizerID: lviv.ID, VenueID: v.ID, Slug: e.slug, Title: e.title, StartsAt: start, EndsAt: start.Add(3 * time.Hour), Genre: "indie"})
		if err != nil {
			t.Fatal(err)
		}
		if e.soldOut {
			tier, err := q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{EventID: created.ID, OrganizerID: lviv.ID, Name: "GA", Price: 40000, Quantity: 50})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, "UPDATE ticket_tiers SET available = 0 WHERE id = $1", tier.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := q.SetEventStatus(ctx, dbgen.SetEventStatusParams{Status: "published", ID: created.ID, OrganizerID: lviv.ID}); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *authSite) seedOrders(t *testing.T, userID int64) {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(s.pool)
	ev, err := q.GetPublicEventBySlug(ctx, "kyiv-philharmonic-event-a")
	if err != nil {
		t.Fatal(err)
	}
	tiers, err := q.ListTicketTiers(ctx, ev.Event.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 23 {
		tier := tiers[i%2]
		qty := int32(i%3 + 1)
		key := pgtype.UUID{Bytes: [16]byte{byte(i + 1), 0xc6}, Valid: true}
		o, err := q.CreateOrder(ctx, dbgen.CreateOrderParams{
			UserID: userID, EventID: ev.Event.ID, TierID: tier.ID, Quantity: qty, UnitPrice: tier.Price, Total: tier.Price * int64(qty), IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateTickets(ctx, dbgen.CreateTicketsParams{OrderID: o.ID, Quantity: qty}); err != nil {
			t.Fatal(err)
		}
	}
	past, err := q.GetPublicEventBySlug(ctx, "kyiv-philharmonic-event-c")
	if err != nil {
		t.Fatal(err)
	}
	pastTier, err := q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{EventID: past.Event.ID, OrganizerID: past.Event.OrganizerID, Name: "Вхідний", Price: 20000, Quantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateOrder(ctx, dbgen.CreateOrderParams{
		UserID: userID, EventID: past.Event.ID, TierID: pastTier.ID, Quantity: 1, UnitPrice: 20000, Total: 20000, IdempotencyKey: pgtype.UUID{Bytes: [16]byte{0xff}, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE orders SET created_at = now() + interval '1 minute' WHERE event_id = $1", past.Event.ID); err != nil {
		t.Fatal(err)
	}
}
