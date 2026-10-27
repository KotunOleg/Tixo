package seed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
)

var seedNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func TestSeedTwiceGivesTheSameCounts(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()

	first, err := Run(ctx, pool, seedNow)
	if err != nil {
		t.Fatal(err)
	}
	want := Report{
		Organizers: int64(len(organizers)), Users: int64(len(organizers)), Venues: int64(len(venues)),
		Events: int64(len(demoEvents)), Artists: int64(len(artists)),
	}
	for _, e := range demoEvents {
		want.Tiers += int64(len(e.tiers))
		want.Lineup += int64(len(e.lineup))
	}
	if first != want {
		t.Errorf("first run = %v, want %v", first, want)
	}

	second, err := Run(ctx, pool, seedNow.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("second run = %v, want the same counts as the first %v", second, first)
	}
}

func TestSeedLeavesUserDataAlone(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	q := dbgen.New(pool)

	mine, err := q.CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: "my-org", Name: "Mine", Contacts: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	hall, err := q.CreateVenue(ctx, dbgen.CreateVenueParams{OrganizerID: mine.ID, Name: "My hall", City: "Odesa", Lat: 46.48, Lng: 30.72, Capacity: 50})
	if err != nil {
		t.Fatal(err)
	}
	taken, err := q.CreateEvent(ctx, dbgen.CreateEventParams{
		OrganizerID: mine.ID, VenueID: hall.ID, Slug: demoEvents[0].slug, Title: "My own show",
		StartsAt: seedNow, EndsAt: seedNow.Add(time.Hour), Genre: "jazz",
	})
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Run(ctx, pool, seedNow)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != int64(len(demoEvents)) {
		t.Errorf("events = %d, want %d: the seed event with a taken slug is skipped, the user's stays", rep.Events, len(demoEvents))
	}

	got, err := q.GetEventForOrganizer(ctx, dbgen.GetEventForOrganizerParams{ID: taken.ID, OrganizerID: mine.ID})
	if err != nil || got.Title != "My own show" || got.VenueID != hall.ID {
		t.Errorf("user's event after seed = %+v, %v; want untouched", got, err)
	}
	lineup, err := q.ListEventArtists(ctx, taken.ID)
	if err != nil || len(lineup) != 0 {
		t.Errorf("seed added %d artists to the user's event (%v)", len(lineup), err)
	}
}

func TestSeedEventsUseKnownGenres(t *testing.T) {
	for _, e := range demoEvents {
		if !events.IsGenre(e.genre) {
			t.Errorf("seed event %s has genre %q outside events.Genres()", e.slug, e.genre)
		}
	}
}

func TestSeedHasPastAndUpcomingPublishedEvents(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	if _, err := Run(ctx, pool, seedNow); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)

	upcoming, err := q.ListUpcomingPublishedEvents(ctx, dbgen.ListUpcomingPublishedEventsParams{After: seedNow, Lim: 100})
	if err != nil {
		t.Fatal(err)
	}
	all, err := q.ListUpcomingPublishedEvents(ctx, dbgen.ListUpcomingPublishedEventsParams{After: seedNow.AddDate(-1, 0, 0), Lim: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(upcoming) < 3 || len(all) <= len(upcoming) {
		t.Errorf("upcoming = %d, all published = %d; want several upcoming and at least one past event", len(upcoming), len(all))
	}
}

func TestParseOwnerAcceptsOnlyDemoOrganizers(t *testing.T) {
	good, err := ParseOwner(" Kyiv-Live = Me@Gmail.com ")
	if err != nil || good != (Owner{Slug: "kyiv-live", Email: "me@gmail.com"}) {
		t.Errorf("ParseOwner = %+v, %v", good, err)
	}
	for _, bad := range []string{"", "kyiv-live", "kyiv-live=", "=me@gmail.com", "kyiv-live=not-an-email", "my-own-org=me@gmail.com"} {
		if _, err := ParseOwner(bad); err == nil {
			t.Errorf("ParseOwner(%q) accepted", bad)
		}
	}
}

func TestAddOwnersGivesRealAccountsTheDemoOrganizers(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	if _, err := Run(ctx, pool, seedNow); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)
	signedIn, err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: "b@gmail.com", Name: "B", GoogleSub: pgtype.Text{String: "sub-b", Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	owners := []Owner{{Slug: "kyiv-live", Email: "a@gmail.com"}, {Slug: "lviv-sound", Email: "b@gmail.com"}}
	for range 2 {
		if err := AddOwners(ctx, pool, owners); err != nil {
			t.Fatal(err)
		}
	}

	var memberships int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE u.email IN ('a@gmail.com', 'b@gmail.com') AND m.role = 'owner'`).Scan(&memberships); err != nil || memberships != 2 {
		t.Errorf("owner memberships for the real accounts = %d, %v; want 2 after two runs", memberships, err)
	}
	pending, err := q.GetUserByEmail(ctx, "a@gmail.com")
	if err != nil || pending.GoogleSub.Valid {
		t.Errorf("a@gmail.com = %+v, %v; want a user without google_sub, linked on first sign-in", pending, err)
	}
	org, err := q.GetOrganizerBySlug(ctx, "lviv-sound")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetMembership(ctx, dbgen.GetMembershipParams{UserID: signedIn.ID, OrganizerID: org.ID}); err != nil {
		t.Errorf("already signed-in b@gmail.com got no membership in lviv-sound: %v", err)
	}
}

func TestEnsureAPIUserCreatesOwnerMembership(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	q := dbgen.New(pool)

	userID, err := EnsureAPIUser(ctx, pool, "ci@example.com", "ci", "qa1-ci", "QA1 CI")
	if err != nil {
		t.Fatal(err)
	}
	org, err := q.GetOrganizerBySlug(ctx, "qa1-ci")
	if err != nil {
		t.Fatal(err)
	}
	m, err := q.GetMembership(ctx, dbgen.GetMembershipParams{UserID: userID, OrganizerID: org.ID})
	if err != nil || m.Role != "owner" {
		t.Errorf("membership = %+v, %v; want owner", m, err)
	}
}

func TestEnsureAPIUserIsIdempotent(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()

	first, err := EnsureAPIUser(ctx, pool, "ci@example.com", "ci", "qa1-ci", "QA1 CI")
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureAPIUser(ctx, pool, "ci@example.com", "ci", "qa1-ci", "QA1 CI")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("EnsureAPIUser twice gave different user ids: %d, %d", first, second)
	}
	var users, organizers, memberships int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email = 'ci@example.com'").Scan(&users); err != nil || users != 1 {
		t.Errorf("users = %d, %v; want 1", users, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM organizers WHERE slug = 'qa1-ci'").Scan(&organizers); err != nil || organizers != 1 {
		t.Errorf("organizers = %d, %v; want 1", organizers, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM memberships").Scan(&memberships); err != nil || memberships != 1 {
		t.Errorf("memberships = %d, %v; want 1", memberships, err)
	}
}

func TestAddOwnersNeedsTheDemoDataFirst(t *testing.T) {
	pool := dbtest.Pool(t)
	if err := AddOwners(context.Background(), pool, []Owner{{Slug: "kyiv-live", Email: "a@gmail.com"}}); err == nil {
		t.Error("AddOwners without seeded organizers succeeded")
	}
	var users int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM users").Scan(&users); err != nil || users != 0 {
		t.Errorf("users after failed AddOwners = %d, %v; want 0", users, err)
	}
}

func TestSeedHasLandingDemoWithSoldOutAndLowStockTiers(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	if _, err := Run(ctx, pool, seedNow); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)
	pub, err := q.GetPublicEventBySlug(ctx, "hardkiss-atlas")
	if err != nil {
		t.Fatal(err)
	}
	tiers, err := q.ListTicketTiers(ctx, pub.Event.ID)
	if err != nil {
		t.Fatal(err)
	}
	left := map[string]int32{}
	for _, tier := range tiers {
		left[tier.Name] = tier.Available
	}
	if left["VIP"] != 0 || left["Танцпол"] != 10 || left["Фан-зона"] != 300 {
		t.Errorf("hardkiss-atlas seats left = %v, want VIP sold out, 10 on the dance floor, fan zone untouched", left)
	}
	if !strings.Contains(pub.Event.Description, "\n\n") || string(pub.OrganizerContacts) == "{}" {
		t.Error("landing demo needs a multi-paragraph description and organizer contacts")
	}
}
