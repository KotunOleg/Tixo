package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
)

type organizer struct {
	slug, name, plan, ownerEmail, ownerName string
	contacts                                map[string]string
}

type venue struct {
	key, organizer, name, address, city string
	lat, lng                            float64
	capacity                            int32
}

type artist struct {
	mbid, name, sortName, typ, country string
}

type tier struct {
	name     string
	price    int64
	quantity int32
}

type event struct {
	slug, organizer, venue, title, description, genre, status string
	dayOffset                                                 int
	lineup                                                    []string
	tiers                                                     []tier
}

var organizers = []organizer{
	{
		slug: "kyiv-live", name: "Kyiv Live", plan: "premium",
		ownerEmail: "owner@kyiv-live.example.com", ownerName: "Олена Коваль",
		contacts: map[string]string{"email": "hello@kyiv-live.example.com", "phone": "+380441234567", "telegram": "@kyivlive"},
	},
	{
		slug: "lviv-sound", name: "Lviv Sound", plan: "free",
		ownerEmail: "owner@lviv-sound.example.com", ownerName: "Andrii Melnyk",
		contacts: map[string]string{"email": "tickets@lviv-sound.example.com"},
	},
}

var venues = []venue{
	{key: "palats-sportu", name: "Палац спорту", address: "Спортивна площа, 1", city: "Київ", lat: 50.4381, lng: 30.5215, capacity: 10000},
	{key: "atlas", name: "Atlas", address: "вул. Січових Стрільців, 37-41", city: "Київ", lat: 50.4547, lng: 30.4942, capacity: 3000},
	{key: "lviv-opera", name: "Львівська національна опера", address: "просп. Свободи, 28", city: "Львів", lat: 49.8440, lng: 24.0263, capacity: 1100},
	{key: "arena-lviv", name: "Арена Львів", address: "вул. Стрийська, 199", city: "Львів", lat: 49.7754, lng: 24.0280, capacity: 34915},
	{key: "kyiv-live-rooftop", organizer: "kyiv-live", name: "Kyiv Live Rooftop", address: "вул. Велика Васильківська, 5", city: "Київ", lat: 50.4380, lng: 30.5200, capacity: 400},
}

var artists = []artist{
	{mbid: "87a417d3-f118-43e0-bece-3de1bd90fce0", name: "Океан Ельзи", sortName: "Okean Elzy", typ: "Group", country: "UA"},
	{mbid: "208334a1-abf0-4791-9705-b45838244b29", name: "DakhaBrakha", sortName: "DakhaBrakha", typ: "Group", country: "UA"},
	{mbid: "b72469ab-0235-4ff5-8e39-09c66dcbe305", name: "The Hardkiss", sortName: "Hardkiss, The", typ: "Group", country: "UA"},
	{mbid: "80b54212-19f5-42ce-bfd7-e08db9c298ae", name: "Go_A", sortName: "Go_A", typ: "Group", country: "UA"},
	{mbid: "fbe2ebe7-30b3-4efe-af11-e0563bb8744a", name: "Onuka", sortName: "Onuka", typ: "Group", country: "UA"},
	{mbid: "628d9735-f961-4eb0-92ee-31d77f9c8b8f", name: "KALUSH", sortName: "KALUSH", typ: "Group", country: "UA"},
	{mbid: "4ab0dc51-a8dd-456a-9f43-8a7845301853", name: "Бумбокс", sortName: "Boombox", typ: "Group", country: "UA"},
	{mbid: "a74b1b7f-71a5-4011-9441-d0b5e4122711", name: "Radiohead", sortName: "Radiohead", typ: "Group", country: "GB"},
	{mbid: "056e4f3e-d505-4dad-8ec1-d04f521cbb56", name: "Daft Punk", sortName: "Daft Punk", typ: "Group", country: "FR"},
	{mbid: "9c9f1380-2516-4fc9-a3e6-f9f61941d090", name: "Muse", sortName: "Muse", typ: "Group", country: "GB"},
	{mbid: "ada7a83c-e3e1-40f1-93f9-3e73dbc9298a", name: "Arctic Monkeys", sortName: "Arctic Monkeys", typ: "Group", country: "GB"},
	{mbid: "cc197bad-dc9c-440d-a5b5-d52ba2e14234", name: "Coldplay", sortName: "Coldplay", typ: "Group", country: "GB"},
	{mbid: "87c5dedd-371d-4a53-9f7f-80522fb7f3cb", name: "Björk", sortName: "Björk", typ: "Person", country: "IS"},
}

var standardTiers = []tier{
	{name: "Фан-зона", price: 120000, quantity: 2000},
	{name: "Танцпол", price: 80000, quantity: 5000},
	{name: "VIP", price: 350000, quantity: 200},
}

var clubTiers = []tier{
	{name: "Вхідний", price: 60000, quantity: 300},
	{name: "VIP", price: 150000, quantity: 50},
}

var demoEvents = []event{
	{
		slug: "okean-elzy-palats-sportu", organizer: "kyiv-live", venue: "palats-sportu", genre: events.GenreRock, status: "published", dayOffset: 21,
		title: "Океан Ельзи. Великий концерт", description: "Головні пісні гурту за 30 років на одній сцені.",
		lineup: []string{"Океан Ельзи"}, tiers: standardTiers,
	},
	{
		slug: "dakhabrakha-lviv-opera", organizer: "lviv-sound", venue: "lviv-opera", genre: events.GenreFolk, status: "published", dayOffset: 35,
		title: "DakhaBrakha в Опері", description: "Етно-хаос у залі Львівської опери.",
		lineup: []string{"DakhaBrakha"}, tiers: []tier{{name: "Партер", price: 150000, quantity: 600}, {name: "Ложа", price: 250000, quantity: 120}},
	},
	{
		slug: "hardkiss-atlas", organizer: "kyiv-live", venue: "atlas", genre: events.GenreAlternative, status: "published", dayOffset: 10,
		title: "The Hardkiss + Onuka", description: "Два сети за один вечір.",
		lineup: []string{"The Hardkiss", "Onuka"}, tiers: standardTiers[:2],
	},
	{
		slug: "go-a-kalush-arena-lviv", organizer: "lviv-sound", venue: "arena-lviv", genre: events.GenreElectronic, status: "published", dayOffset: 60,
		title: "Go_A × KALUSH: Open Air", description: "Великий літній опен-ейр на Арені Львів.",
		lineup: []string{"KALUSH", "Go_A"}, tiers: standardTiers,
	},
	{
		slug: "boombox-rooftop", organizer: "kyiv-live", venue: "kyiv-live-rooftop", genre: events.GenrePop, status: "published", dayOffset: 5,
		title: "Бумбокс: акустика на даху", description: "Камерний акустичний вечір.",
		lineup: []string{"Бумбокс"}, tiers: clubTiers,
	},
	{
		slug: "brit-night-palats-sportu", organizer: "kyiv-live", venue: "palats-sportu", genre: events.GenreIndie, status: "published", dayOffset: 90,
		title: "Brit Night: Arctic Monkeys, Muse, Radiohead", description: "Ніч британського року.",
		lineup: []string{"Arctic Monkeys", "Muse", "Radiohead"}, tiers: standardTiers,
	},
	{
		slug: "daft-punk-tribute-atlas", organizer: "kyiv-live", venue: "atlas", genre: events.GenreElectronic, status: "draft", dayOffset: 120,
		title: "Daft Punk Tribute Night", description: "Чернетка: програма ще формується.",
		lineup: []string{"Daft Punk"}, tiers: clubTiers,
	},
	{
		slug: "coldplay-arena-lviv", organizer: "lviv-sound", venue: "arena-lviv", genre: events.GenrePop, status: "cancelled", dayOffset: 45,
		title: "Coldplay у Львові", description: "Концерт скасовано організатором.",
		lineup: []string{"Coldplay"}, tiers: standardTiers,
	},
	{
		slug: "bjork-lviv-opera", organizer: "lviv-sound", venue: "lviv-opera", genre: events.GenreElectronic, status: "published", dayOffset: -30,
		title: "Björk: Orchestral", description: "Минула подія — для історії й аналітики.",
		lineup: []string{"Björk"}, tiers: []tier{{name: "Партер", price: 300000, quantity: 600}},
	},
}

type Report struct {
	Organizers, Users, Venues, Events, Artists, Tiers, Lineup int64
}

func (r Report) String() string {
	return fmt.Sprintf("organizers %d, users %d, venues %d, events %d, artists %d, ticket tiers %d, lineup rows %d",
		r.Organizers, r.Users, r.Venues, r.Events, r.Artists, r.Tiers, r.Lineup)
}

func Run(ctx context.Context, pool *pgxpool.Pool, now time.Time) (Report, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)

	orgIDs := make(map[string]int64, len(organizers))
	for _, o := range organizers {
		contacts, err := json.Marshal(o.contacts)
		if err != nil {
			return Report{}, err
		}
		id, err := q.SeedOrganizer(ctx, dbgen.SeedOrganizerParams{Slug: o.slug, Name: o.name, Contacts: contacts, Plan: o.plan})
		if err != nil {
			return Report{}, fmt.Errorf("organizer %s: %w", o.slug, err)
		}
		orgIDs[o.slug] = id
		userID, err := q.SeedUser(ctx, dbgen.SeedUserParams{Email: o.ownerEmail, Name: o.ownerName})
		if err != nil {
			return Report{}, fmt.Errorf("user %s: %w", o.ownerEmail, err)
		}
		if err := q.SeedMembership(ctx, dbgen.SeedMembershipParams{UserID: userID, OrganizerID: id, Role: "owner"}); err != nil {
			return Report{}, fmt.Errorf("membership %s: %w", o.slug, err)
		}
	}

	venueIDs := make(map[string]int64, len(venues))
	for _, v := range venues {
		var owner pgtype.Int8
		if v.organizer != "" {
			owner = pgtype.Int8{Int64: orgIDs[v.organizer], Valid: true}
		}
		id, err := q.SeedVenue(ctx, dbgen.SeedVenueParams{
			OrganizerID: owner, Name: v.name, Address: v.address, City: v.city, Lat: v.lat, Lng: v.lng, Capacity: v.capacity,
		})
		if err != nil {
			return Report{}, fmt.Errorf("venue %s: %w", v.key, err)
		}
		venueIDs[v.key] = id
	}

	artistIDs := make(map[string]int64, len(artists))
	for _, a := range artists {
		var mbid pgtype.UUID
		if err := mbid.Scan(a.mbid); err != nil {
			return Report{}, fmt.Errorf("artist %s: %w", a.name, err)
		}
		id, err := q.SeedArtist(ctx, dbgen.SeedArtistParams{
			Mbid: mbid, Name: a.name, SortName: a.sortName,
			Type: pgtype.Text{String: a.typ, Valid: true}, Country: pgtype.Text{String: a.country, Valid: a.country != ""},
		})
		if err != nil {
			return Report{}, fmt.Errorf("artist %s: %w", a.name, err)
		}
		artistIDs[a.name] = id
	}

	day := now.UTC().Truncate(24 * time.Hour)
	for _, e := range demoEvents {
		starts := day.AddDate(0, 0, e.dayOffset).Add(16 * time.Hour)
		eventID, err := q.SeedEvent(ctx, dbgen.SeedEventParams{
			OrganizerID: orgIDs[e.organizer], VenueID: venueIDs[e.venue], Slug: e.slug, Title: e.title,
			Description: e.description, StartsAt: starts, EndsAt: starts.Add(3 * time.Hour), Genre: e.genre,
			PriceFrom: pgtype.Int8{Int64: minPrice(e.tiers), Valid: true}, Status: e.status,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return Report{}, fmt.Errorf("event %s: %w", e.slug, err)
		}
		for i, name := range e.lineup {
			_, err := q.SetEventArtist(ctx, dbgen.SetEventArtistParams{
				EventID: eventID, OrganizerID: orgIDs[e.organizer], ArtistID: artistIDs[name],
				Position: int32(i), IsHeadliner: i == 0,
			})
			if err != nil {
				return Report{}, fmt.Errorf("lineup %s/%s: %w", e.slug, name, err)
			}
		}
		for _, t := range e.tiers {
			err := q.SeedTicketTier(ctx, dbgen.SeedTicketTierParams{EventID: eventID, Name: t.name, Price: t.price, Quantity: t.quantity})
			if err != nil {
				return Report{}, fmt.Errorf("tier %s/%s: %w", e.slug, t.name, err)
			}
		}
	}

	rep, err := count(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	return rep, tx.Commit(ctx)
}

func minPrice(tiers []tier) int64 {
	lowest := tiers[0].price
	for _, t := range tiers[1:] {
		lowest = min(lowest, t.price)
	}
	return lowest
}

func count(ctx context.Context, tx pgx.Tx) (Report, error) {
	var r Report
	tables := []struct {
		name string
		dst  *int64
	}{
		{"organizers", &r.Organizers}, {"users", &r.Users}, {"venues", &r.Venues}, {"events", &r.Events},
		{"artists", &r.Artists}, {"ticket_tiers", &r.Tiers}, {"event_artists", &r.Lineup},
	}
	parts := make([]string, len(tables))
	dsts := make([]any, len(tables))
	for i, t := range tables {
		parts[i] = "(SELECT count(*) FROM " + t.name + ")"
		dsts[i] = t.dst
	}
	err := tx.QueryRow(ctx, "SELECT "+strings.Join(parts, ", ")).Scan(dsts...)
	return r, err
}

type Owner struct {
	Slug  string
	Email string
}

var ErrBadOwner = errors.New(`owner must look like <demo-organizer-slug>=<email>`)

func ParseOwner(raw string) (Owner, error) {
	slug, email, ok := strings.Cut(raw, "=")
	o := Owner{Slug: strings.ToLower(strings.TrimSpace(slug)), Email: strings.ToLower(strings.TrimSpace(email))}
	if !ok || o.Slug == "" || !strings.Contains(o.Email, "@") {
		return Owner{}, fmt.Errorf("%w, got %q", ErrBadOwner, raw)
	}
	if !slices.ContainsFunc(organizers, func(d organizer) bool { return d.slug == o.Slug }) {
		return Owner{}, fmt.Errorf("%w: %q is not a demo organizer (%s)", ErrBadOwner, o.Slug, demoSlugs())
	}
	return o, nil
}

func demoSlugs() string {
	slugs := make([]string, len(organizers))
	for i, o := range organizers {
		slugs[i] = o.slug
	}
	return strings.Join(slugs, ", ")
}

func AddOwners(ctx context.Context, pool *pgxpool.Pool, owners []Owner) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	for _, o := range owners {
		org, err := q.GetOrganizerBySlug(ctx, o.Slug)
		if err != nil {
			return fmt.Errorf("organizer %s: %w (run seed without --owner first)", o.Slug, err)
		}
		name, _, _ := strings.Cut(o.Email, "@")
		userID, err := q.SeedUser(ctx, dbgen.SeedUserParams{Email: o.Email, Name: name})
		if err != nil {
			return fmt.Errorf("user %s: %w", o.Email, err)
		}
		if err := q.SeedOwner(ctx, dbgen.SeedOwnerParams{UserID: userID, OrganizerID: org.ID}); err != nil {
			return fmt.Errorf("owner %s of %s: %w", o.Email, o.Slug, err)
		}
	}
	return tx.Commit(ctx)
}
