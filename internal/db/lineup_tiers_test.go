package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func mustArtist(t *testing.T, q *dbgen.Queries, name string) dbgen.Artist {
	t.Helper()
	a, err := q.CreateArtist(context.Background(), dbgen.CreateArtistParams{Name: name, SortName: name})
	if err != nil {
		t.Fatalf("create artist %s: %v", name, err)
	}
	return a
}

func mustTier(t *testing.T, q *dbgen.Queries, organizerID, eventID int64, name string, quantity int32) dbgen.TicketTier {
	t.Helper()
	tier, err := q.CreateTicketTier(context.Background(), dbgen.CreateTicketTierParams{
		EventID: eventID, OrganizerID: organizerID, Name: name, Price: 50000, Quantity: quantity,
	})
	if err != nil {
		t.Fatalf("create tier %s: %v", name, err)
	}
	return tier
}

func TestTicketTierAvailableStaysWithinQuantity(t *testing.T) {
	pool, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	ev := mustEvent(t, q, a.ID, mustVenue(t, q, a.ID, "A hall").ID, "show")
	tier := mustTier(t, q, a.ID, ev.ID, "Fan zone", 10)
	if tier.Available != 10 {
		t.Fatalf("new tier available = %d, want quantity 10", tier.Available)
	}

	for name, sql := range map[string]string{
		"oversold below zero": `UPDATE ticket_tiers SET available = -1 WHERE id = $1`,
		"more than quantity":  `UPDATE ticket_tiers SET available = quantity + 1 WHERE id = $1`,
	} {
		_, err := pool.Exec(ctx, sql, tier.ID)
		wantPgCode(t, name, err, checkViolation)
	}

	if _, err := pool.Exec(ctx, `UPDATE ticket_tiers SET available = available - 7 WHERE id = $1`, tier.ID); err != nil {
		t.Fatal(err)
	}
	grown, err := q.UpdateTicketTier(ctx, dbgen.UpdateTicketTierParams{ID: tier.ID, OrganizerID: a.ID, Name: tier.Name, Price: tier.Price, Quantity: 15})
	if err != nil || grown.Available != 8 {
		t.Errorf("raise quantity 10→15 with 7 sold: available = %d, err = %v; want 8", grown.Available, err)
	}
	_, err = q.UpdateTicketTier(ctx, dbgen.UpdateTicketTierParams{ID: tier.ID, OrganizerID: a.ID, Name: tier.Name, Price: tier.Price, Quantity: 5})
	wantPgCode(t, "quantity below tickets sold", err, checkViolation)

	n, err := q.DeleteUnsoldTicketTier(ctx, dbgen.DeleteUnsoldTicketTierParams{ID: tier.ID, OrganizerID: a.ID})
	if err != nil || n != 0 {
		t.Errorf("delete tier with sold tickets: rows = %d, err = %v; want 0", n, err)
	}
	_, err = q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{EventID: ev.ID, OrganizerID: a.ID, Name: "Fan zone", Price: 1, Quantity: 1})
	wantPgCode(t, "duplicate tier name", err, uniqueViolation)
}

func TestEventWithTicketTiersCannotBeDeleted(t *testing.T) {
	pool, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	venue := mustVenue(t, q, a.ID, "A hall")
	artist := mustArtist(t, q, "Okean Elzy")

	withTiers := mustEvent(t, q, a.ID, venue.ID, "with-tiers")
	mustTier(t, q, a.ID, withTiers.ID, "Dance floor", 100)
	_, err := q.DeleteEvent(ctx, dbgen.DeleteEventParams{ID: withTiers.ID, OrganizerID: a.ID})
	wantPgCode(t, "delete event with tiers", err, foreignKeyViolation)

	lineupOnly := mustEvent(t, q, a.ID, venue.ID, "lineup-only")
	if _, err := q.SetEventArtist(ctx, dbgen.SetEventArtistParams{EventID: lineupOnly.ID, OrganizerID: a.ID, ArtistID: artist.ID}); err != nil {
		t.Fatal(err)
	}
	if n, err := q.DeleteEvent(ctx, dbgen.DeleteEventParams{ID: lineupOnly.ID, OrganizerID: a.ID}); err != nil || n != 1 {
		t.Fatalf("delete event with only a lineup: rows = %d, err = %v", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_artists WHERE event_id = $1`, lineupOnly.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("lineup rows left after event delete = %d, err = %v", left, err)
	}
}

func TestOrganizerCannotEditAnotherOrganizersLineupOrTiers(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	b := mustOrganizer(t, q, "org-b")
	ev := mustEvent(t, q, a.ID, mustVenue(t, q, a.ID, "A hall").ID, "a-show")
	tier := mustTier(t, q, a.ID, ev.ID, "Standard", 50)
	artist := mustArtist(t, q, "DakhaBrakha")
	if _, err := q.SetEventArtist(ctx, dbgen.SetEventArtistParams{EventID: ev.ID, OrganizerID: a.ID, ArtistID: artist.ID, IsHeadliner: true}); err != nil {
		t.Fatal(err)
	}

	intruder := mustArtist(t, q, "Intruder")
	if n, err := q.SetEventArtist(ctx, dbgen.SetEventArtistParams{EventID: ev.ID, OrganizerID: b.ID, ArtistID: intruder.ID}); err != nil || n != 0 {
		t.Errorf("B adds to A's lineup: rows = %d, err = %v; want 0", n, err)
	}
	if n, err := q.SetEventArtist(ctx, dbgen.SetEventArtistParams{EventID: ev.ID, OrganizerID: b.ID, ArtistID: artist.ID}); err != nil || n != 0 {
		t.Errorf("B demotes A's headliner: rows = %d, err = %v; want 0", n, err)
	}
	if n, err := q.RemoveEventArtist(ctx, dbgen.RemoveEventArtistParams{EventID: ev.ID, OrganizerID: b.ID, ArtistID: artist.ID}); err != nil || n != 0 {
		t.Errorf("B removes from A's lineup: rows = %d, err = %v; want 0", n, err)
	}
	lineup, err := q.ListEventArtists(ctx, ev.ID)
	if err != nil || len(lineup) != 1 || !lineup[0].IsHeadliner {
		t.Errorf("A's lineup after B's attempts = %+v, %v; want one headliner", lineup, err)
	}

	_, err = q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{EventID: ev.ID, OrganizerID: b.ID, Name: "Free", Quantity: 1000})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("B adds a tier to A's event: err = %v, want no rows", err)
	}
	_, err = q.UpdateTicketTier(ctx, dbgen.UpdateTicketTierParams{ID: tier.ID, OrganizerID: b.ID, Name: tier.Name, Price: 0, Quantity: tier.Quantity})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("B reprices A's tier: err = %v, want no rows", err)
	}
	if n, err := q.DeleteUnsoldTicketTier(ctx, dbgen.DeleteUnsoldTicketTierParams{ID: tier.ID, OrganizerID: b.ID}); err != nil || n != 0 {
		t.Errorf("B deletes A's tier: rows = %d, err = %v; want 0", n, err)
	}
	tiers, err := q.ListTicketTiers(ctx, ev.ID)
	if err != nil || len(tiers) != 1 || tiers[0].Price != tier.Price {
		t.Errorf("A's tiers after B's attempts = %+v, %v; want unchanged", tiers, err)
	}
}

func TestSetEventArtistIsAnUpsert(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	ev := mustEvent(t, q, a.ID, mustVenue(t, q, a.ID, "A hall").ID, "show")
	artist := mustArtist(t, q, "Jamala")

	for _, headliner := range []bool{false, true} {
		if _, err := q.SetEventArtist(ctx, dbgen.SetEventArtistParams{EventID: ev.ID, OrganizerID: a.ID, ArtistID: artist.ID, Position: 2, IsHeadliner: headliner}); err != nil {
			t.Fatal(err)
		}
	}
	lineup, err := q.ListEventArtists(ctx, ev.ID)
	if err != nil || len(lineup) != 1 || !lineup[0].IsHeadliner || lineup[0].Position != 2 {
		t.Errorf("lineup = %+v, %v; want one row updated to headliner", lineup, err)
	}
}

func TestSearchArtistsMatchesSubstringAndTreatsWildcardsLiterally(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	for _, name := range []string{"Radiohead", "Head Automatica", "The Hardkiss", "100% Funk", "a_b"} {
		mustArtist(t, q, name)
	}

	cases := []struct {
		query string
		want  []string
	}{
		{"head", []string{"Head Automatica", "Radiohead"}},
		{"RADIO", []string{"Radiohead"}},
		{"%", []string{"100% Funk"}},
		{"_", []string{"a_b"}},
		{"nothing-like-this", nil},
	}
	for _, c := range cases {
		got, err := q.SearchArtists(ctx, dbgen.SearchArtistsParams{Query: c.query, Lim: 10})
		if err != nil {
			t.Fatalf("search %q: %v", c.query, err)
		}
		names := make(map[string]bool, len(got))
		for _, a := range got {
			names[a.Name] = true
		}
		if len(got) != len(c.want) {
			t.Errorf("search %q = %v, want %v", c.query, keys(names), c.want)
			continue
		}
		for _, w := range c.want {
			if !names[w] {
				t.Errorf("search %q = %v, missing %q", c.query, keys(names), w)
			}
		}
	}

	limited, err := q.SearchArtists(ctx, dbgen.SearchArtistsParams{Query: "a", Lim: 2})
	if err != nil || len(limited) != 2 {
		t.Errorf("search with limit 2 returned %d rows, err = %v", len(limited), err)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
