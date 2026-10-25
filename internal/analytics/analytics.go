package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/features"
	"github.com/KotunOleg/Tixo/internal/organizers"
)

const (
	DefaultDays = 30
	MinDays     = 1
	MaxDays     = 90
	TopEvents   = 10

	dayKey = "2006-01-02"
)

var (
	ErrFeatureDisabled = errors.New("sales analytics is not enabled for this organizer")
	ErrInvalidDays     = fmt.Errorf("days must be between %d and %d", MinDays, MaxDays)

	kyiv = mustLocation("Europe/Kyiv")
)

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type Day struct {
	Date    time.Time
	Tickets int64
	Revenue int64
}

type EventSales struct {
	ID          int64
	Slug        string
	Title       string
	Tickets     int64
	Revenue     int64
	OthersCount int
}

func (e EventSales) IsOthers() bool { return e.OthersCount > 0 }

type GenreCount struct {
	Genre  string
	Events int64
}

type Report struct {
	Days            int
	From            time.Time
	To              time.Time
	Tickets         int64
	Revenue         int64
	PublishedEvents int64
	ByDay           []Day
	ByEvent         []EventSales
	ByGenre         []GenreCount
}

func (r Report) HasSales() bool { return r.Tickets > 0 }

type Service struct {
	q     *dbgen.Queries
	flags *features.Service
	now   func() time.Time
}

func New(pool *pgxpool.Pool, flags *features.Service) *Service {
	return &Service{q: dbgen.New(pool), flags: flags, now: time.Now}
}

func (s *Service) Allowed(ctx context.Context, m organizers.Membership) (bool, error) {
	return s.flags.Enabled(ctx, m.Organizer.ID, features.SalesAnalytics)
}

func (s *Service) Report(ctx context.Context, m organizers.Membership, days int) (Report, error) {
	if days < MinDays || days > MaxDays {
		return Report{}, ErrInvalidDays
	}
	on, err := s.Allowed(ctx, m)
	if err != nil {
		return Report{}, err
	}
	if !on {
		return Report{}, ErrFeatureDisabled
	}

	orgID := m.Organizer.ID
	today := s.now().In(kyiv)
	from := time.Date(today.Year(), today.Month(), today.Day()-(days-1), 0, 0, 0, 0, kyiv)
	until := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, kyiv)
	r := Report{Days: days, From: from, To: time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, kyiv)}

	dayRows, err := s.q.SalesByDayForOrganizer(ctx, dbgen.SalesByDayForOrganizerParams{OrganizerID: orgID, Since: from, Until: until})
	if err != nil {
		return Report{}, fmt.Errorf("sales by day: %w", err)
	}
	sold := make(map[string]dbgen.SalesByDayForOrganizerRow, len(dayRows))
	for _, row := range dayRows {
		sold[row.Day.Time.Format(dayKey)] = row
	}
	r.ByDay = make([]Day, 0, days)
	for i := range days {
		d := time.Date(from.Year(), from.Month(), from.Day()+i, 0, 0, 0, 0, kyiv)
		row := sold[d.Format(dayKey)]
		r.ByDay = append(r.ByDay, Day{Date: d, Tickets: row.Tickets, Revenue: row.Revenue})
		r.Tickets += row.Tickets
		r.Revenue += row.Revenue
	}

	eventRows, err := s.q.SalesByEventForOrganizer(ctx, dbgen.SalesByEventForOrganizerParams{OrganizerID: orgID, Since: from, Until: until})
	if err != nil {
		return Report{}, fmt.Errorf("sales by event: %w", err)
	}
	r.ByEvent = make([]EventSales, 0, min(len(eventRows), TopEvents+1))
	for i, row := range eventRows {
		if i < TopEvents {
			r.ByEvent = append(r.ByEvent, EventSales{ID: row.ID, Slug: row.Slug, Title: row.Title, Tickets: row.Tickets, Revenue: row.Revenue})
			continue
		}
		if i == TopEvents {
			r.ByEvent = append(r.ByEvent, EventSales{})
		}
		others := &r.ByEvent[TopEvents]
		others.Tickets += row.Tickets
		others.Revenue += row.Revenue
		others.OthersCount++
	}

	genreRows, err := s.q.EventsByGenreForOrganizer(ctx, orgID)
	if err != nil {
		return Report{}, fmt.Errorf("events by genre: %w", err)
	}
	r.ByGenre = make([]GenreCount, len(genreRows))
	for i, row := range genreRows {
		r.ByGenre[i] = GenreCount{Genre: row.Genre, Events: row.Events}
	}

	if r.PublishedEvents, err = s.q.CountPublishedEventsForOrganizer(ctx, orgID); err != nil {
		return Report{}, fmt.Errorf("published events: %w", err)
	}
	return r, nil
}
