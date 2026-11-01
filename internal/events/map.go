package events

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	FieldFrom     = "from"
	FieldTo       = "to"
	FieldMaxPrice = "max_price"

	MapEventsPerVenue = 5
	mapEventsLimit    = 2000
	mapDateLayout     = "2006-01-02"
	maxPriceUAH       = 1_000_000_000
	kopecksPerUAH     = 100
)

var kyiv = mustLocation("Europe/Kyiv")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type MapQuery struct {
	From     string
	To       string
	Genre    string
	MaxPrice string
}

type MapEvent struct {
	Slug      string
	Title     string
	Genre     string
	StartsAt  time.Time
	PriceFrom *int64
}

type MapVenue struct {
	ID      int64
	Name    string
	City    string
	Address string
	Lat     float64
	Lng     float64
	Events  []MapEvent
	More    int
}

func (s *Service) Map(ctx context.Context, now time.Time, q MapQuery) ([]MapVenue, error) {
	params, err := mapParams(now, q)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListMapEvents(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list map events: %w", err)
	}
	return groupByVenue(rows), nil
}

func mapParams(now time.Time, q MapQuery) (dbgen.ListMapEventsParams, error) {
	fields := map[string]string{}
	params := dbgen.ListMapEventsParams{After: now, Lim: mapEventsLimit}

	from, fromOK := parseMapDay(strings.TrimSpace(q.From), FieldFrom, fields)
	if fromOK && from.After(now) {
		params.After = from
	}
	to, toOK := parseMapDay(strings.TrimSpace(q.To), FieldTo, fields)
	if toOK {
		if fromOK && to.Before(from) {
			fields[FieldTo] = CodeRange
		}
		params.Before = pgtype.Timestamptz{Time: to.AddDate(0, 0, 1), Valid: true}
	}

	if genre := strings.TrimSpace(q.Genre); genre != "" {
		if !IsGenre(genre) {
			fields[FieldGenre] = CodeInvalid
		}
		params.Genre = pgtype.Text{String: genre, Valid: true}
	}

	if raw := strings.TrimSpace(q.MaxPrice); raw != "" {
		uah, err := strconv.ParseInt(raw, 10, 64)
		switch {
		case err != nil:
			fields[FieldMaxPrice] = CodeFormat
		case uah < 0 || uah > maxPriceUAH:
			fields[FieldMaxPrice] = CodeRange
		}
		params.MaxPrice = pgtype.Int8{Int64: uah * kopecksPerUAH, Valid: true}
	}

	if len(fields) > 0 {
		return dbgen.ListMapEventsParams{}, &ValidationError{Fields: fields}
	}
	return params, nil
}

func parseMapDay(raw, field string, fields map[string]string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation(mapDateLayout, raw, kyiv)
	if err != nil {
		fields[field] = CodeFormat
		return time.Time{}, false
	}
	return day, true
}

func groupByVenue(rows []dbgen.ListMapEventsRow) []MapVenue {
	var out []MapVenue
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].ID != r.VenueID {
			out = append(out, MapVenue{
				ID: r.VenueID, Name: r.VenueName, City: r.VenueCity, Address: r.VenueAddress, Lat: r.VenueLat, Lng: r.VenueLng,
			})
		}
		v := &out[len(out)-1]
		if len(v.Events) == MapEventsPerVenue {
			v.More++
			continue
		}
		e := MapEvent{Slug: r.Slug, Title: r.Title, Genre: r.Genre, StartsAt: r.StartsAt}
		if r.HasPrice {
			price := r.PriceFrom
			e.PriceFrom = &price
		}
		v.Events = append(v.Events, e)
	}
	return out
}
