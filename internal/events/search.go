package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	FieldQuery = "query"
	FieldCity  = "city"
	FieldLimit = "limit"

	SearchDefaultLimit = 5
	SearchMaxLimit     = 10
	searchTextMaxLen   = 100
)

type SearchQuery struct {
	Text     string
	City     string
	Genre    string
	From     string
	To       string
	MaxPrice string
	Limit    int
}

type SearchResult struct {
	Slug      string
	Title     string
	Genre     string
	StartsAt  time.Time
	PriceFrom *int64
	VenueName string
	VenueCity string
}

func (s *Service) Search(ctx context.Context, now time.Time, q SearchQuery) ([]SearchResult, error) {
	params, err := searchParams(now, q)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.SearchPublicEvents(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("search public events: %w", err)
	}
	out := make([]SearchResult, 0, len(rows))
	for _, r := range rows {
		res := SearchResult{
			Slug: r.Slug, Title: r.Title, Genre: r.Genre, StartsAt: r.StartsAt, VenueName: r.VenueName, VenueCity: r.VenueCity,
		}
		if r.HasPrice {
			price := r.PriceFrom
			res.PriceFrom = &price
		}
		out = append(out, res)
	}
	return out, nil
}

func searchParams(now time.Time, q SearchQuery) (dbgen.SearchPublicEventsParams, error) {
	fields := map[string]string{}
	mp, err := mapParams(now, MapQuery{From: q.From, To: q.To, Genre: q.Genre, MaxPrice: q.MaxPrice})
	var verr *ValidationError
	if errors.As(err, &verr) {
		fields = verr.Fields
	} else if err != nil {
		return dbgen.SearchPublicEventsParams{}, err
	}

	params := dbgen.SearchPublicEventsParams{
		After: mp.After, Before: mp.Before, Genre: mp.Genre, MaxPrice: mp.MaxPrice, Lim: SearchDefaultLimit,
	}
	if text := strings.TrimSpace(q.Text); text != "" {
		if utf8.RuneCountInString(text) > searchTextMaxLen {
			fields[FieldQuery] = CodeLength
		}
		params.Query = pgtype.Text{String: text, Valid: true}
	}
	if city := strings.TrimSpace(q.City); city != "" {
		if utf8.RuneCountInString(city) > searchTextMaxLen {
			fields[FieldCity] = CodeLength
		}
		params.City = pgtype.Text{String: city, Valid: true}
	}
	switch {
	case q.Limit < 0:
		fields[FieldLimit] = CodeRange
	case q.Limit > SearchMaxLimit:
		params.Lim = SearchMaxLimit
	case q.Limit > 0:
		params.Lim = int32(q.Limit)
	}

	if len(fields) > 0 {
		return dbgen.SearchPublicEventsParams{}, &ValidationError{Fields: fields}
	}
	return params, nil
}
