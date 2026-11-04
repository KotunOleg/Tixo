package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/orders"
)

const (
	NameSearchEvents = "search_events"
	NameGetEvent     = "get_event"
	NameListVenues   = "list_venues"
	NameBookTicket   = "book_ticket"

	CodeInvalid    = "invalid"
	CodeNotFound   = "not_found"
	CodeNotOnSale  = "not_on_sale"
	CodeSoldOut    = "sold_out"
	CodeKeyReused  = "key_reused"
	CodeInternal   = "internal"
	CodeNoAccount  = "unauthenticated"
	keyMinLen      = 8
	keyMaxLen      = 64
	venueEventsMax = 3
	kopecksPerUAH  = 100
)

var (
	kyiv             = mustLocation("Europe/Kyiv")
	bookingNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://tixo/tools/book_ticket"))
)

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type Tools struct {
	events  *events.Service
	orders  *orders.Service
	baseURL string
	now     func() time.Time
}

func New(ev *events.Service, ord *orders.Service, baseURL url.URL, now func() time.Time) *Tools {
	return &Tools{events: ev, orders: ord, baseURL: strings.TrimRight(baseURL.String(), "/"), now: now}
}

type SearchEventsInput struct {
	Query       string `json:"query,omitempty" jsonschema:"free text matched against event title, artist, venue name and city"`
	City        string `json:"city,omitempty" jsonschema:"city name as written on the site, e.g. Kyiv or Київ"`
	Genre       string `json:"genre,omitempty" jsonschema:"one genre code: rock, pop, indie, alternative, electronic, hip-hop, folk, jazz, classical, metal"`
	DateFrom    string `json:"date_from,omitempty" jsonschema:"first day, YYYY-MM-DD in Kyiv time"`
	DateTo      string `json:"date_to,omitempty" jsonschema:"last day inclusive, YYYY-MM-DD in Kyiv time"`
	MaxPriceUAH *int64 `json:"max_price_uah,omitempty" jsonschema:"upper bound for the cheapest ticket, whole hryvnias"`
	Limit       int    `json:"limit,omitempty" jsonschema:"how many events to return, 1-10, default 5"`
}

type EventSummary struct {
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	StartsAt     string   `json:"starts_at"`
	Genre        string   `json:"genre"`
	Venue        string   `json:"venue"`
	City         string   `json:"city"`
	PriceFromUAH *float64 `json:"price_from_uah,omitempty"`
	URL          string   `json:"url"`
}

type SearchEventsOutput struct {
	Events []EventSummary `json:"events"`
}

func (t *Tools) SearchEvents(ctx context.Context, in SearchEventsInput) (SearchEventsOutput, error) {
	q := events.SearchQuery{Text: in.Query, City: in.City, Genre: in.Genre, From: in.DateFrom, To: in.DateTo, Limit: in.Limit}
	if in.MaxPriceUAH != nil {
		q.MaxPrice = strconv.FormatInt(*in.MaxPriceUAH, 10)
	}
	rows, err := t.events.Search(ctx, t.now(), q)
	if err != nil {
		return SearchEventsOutput{}, toolError(err)
	}
	out := SearchEventsOutput{Events: make([]EventSummary, 0, len(rows))}
	for _, r := range rows {
		out.Events = append(out.Events, EventSummary{
			Slug: r.Slug, Title: r.Title, StartsAt: kyivTime(r.StartsAt), Genre: r.Genre, Venue: r.VenueName, City: r.VenueCity,
			PriceFromUAH: uahPtr(r.PriceFrom), URL: t.eventURL(r.Slug),
		})
	}
	return out, nil
}

type GetEventInput struct {
	Slug string `json:"slug" jsonschema:"event slug from search_events"`
}

type Tier struct {
	TierID    int64   `json:"tier_id"`
	Name      string  `json:"name"`
	PriceUAH  float64 `json:"price_uah"`
	Available int32   `json:"available"`
}

type Event struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	OnSale      bool     `json:"on_sale"`
	Description string   `json:"description"`
	Genre       string   `json:"genre"`
	StartsAt    string   `json:"starts_at"`
	EndsAt      string   `json:"ends_at"`
	Venue       string   `json:"venue"`
	Address     string   `json:"address"`
	City        string   `json:"city"`
	Lineup      []string `json:"lineup"`
	Tiers       []Tier   `json:"tiers"`
	URL         string   `json:"url"`
}

func (t *Tools) GetEvent(ctx context.Context, in GetEventInput) (Event, error) {
	p, err := t.events.PublicBySlug(ctx, strings.TrimSpace(in.Slug))
	if err != nil {
		return Event{}, toolError(err)
	}
	e := Event{
		Slug: p.Event.Slug, Title: p.Event.Title, Status: p.Event.Status, Description: p.Event.Description, Genre: p.Event.Genre,
		StartsAt: kyivTime(p.Event.StartsAt), EndsAt: kyivTime(p.Event.EndsAt),
		Venue: p.Venue.Name, Address: p.Venue.Address, City: p.Venue.City,
		Lineup: make([]string, 0, len(p.Lineup)), Tiers: make([]Tier, 0, len(p.Tiers)), URL: t.eventURL(p.Event.Slug),
	}
	for _, a := range p.Lineup {
		e.Lineup = append(e.Lineup, a.Artist.Name)
	}
	upcoming := p.Event.Status == events.StatusPublished && p.Event.StartsAt.After(t.now())
	for _, tier := range p.Tiers {
		e.Tiers = append(e.Tiers, Tier{TierID: tier.ID, Name: tier.Name, PriceUAH: uah(tier.Price), Available: tier.Available})
		if upcoming && tier.Available > 0 {
			e.OnSale = true
		}
	}
	return e, nil
}

type ListVenuesInput struct {
	City string `json:"city,omitempty" jsonschema:"only venues in this city"`
}

type VenueEvent struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
}

type Venue struct {
	Name           string       `json:"name"`
	City           string       `json:"city"`
	Address        string       `json:"address"`
	UpcomingEvents int          `json:"upcoming_events"`
	Next           []VenueEvent `json:"next"`
}

type ListVenuesOutput struct {
	Venues []Venue `json:"venues"`
}

func (t *Tools) ListVenues(ctx context.Context, in ListVenuesInput) (ListVenuesOutput, error) {
	rows, err := t.events.Map(ctx, t.now(), events.MapQuery{})
	if err != nil {
		return ListVenuesOutput{}, toolError(err)
	}
	city := strings.ToLower(strings.TrimSpace(in.City))
	out := ListVenuesOutput{Venues: []Venue{}}
	for _, v := range rows {
		if city != "" && !strings.Contains(strings.ToLower(v.City), city) {
			continue
		}
		next := make([]VenueEvent, 0, venueEventsMax)
		for _, e := range v.Events[:min(len(v.Events), venueEventsMax)] {
			next = append(next, VenueEvent{Slug: e.Slug, Title: e.Title, StartsAt: kyivTime(e.StartsAt)})
		}
		slices.SortFunc(next, func(a, b VenueEvent) int { return strings.Compare(a.StartsAt, b.StartsAt) })
		out.Venues = append(out.Venues, Venue{
			Name: v.Name, City: v.City, Address: v.Address, UpcomingEvents: len(v.Events) + v.More, Next: next,
		})
	}
	return out, nil
}

type BookTicketInput struct {
	EventSlug      string `json:"event_slug" jsonschema:"event slug from search_events or get_event"`
	TierID         int64  `json:"tier_id" jsonschema:"tier_id from get_event"`
	Quantity       int    `json:"quantity" jsonschema:"number of tickets, 1-10"`
	IdempotencyKey string `json:"idempotency_key" jsonschema:"8-64 characters chosen by the caller; retrying with the same key returns the same order instead of buying twice"`
}

type Order struct {
	OrderID  int64   `json:"order_id"`
	Status   string  `json:"status"`
	Event    string  `json:"event"`
	Tier     string  `json:"tier"`
	Quantity int32   `json:"quantity"`
	TotalUAH float64 `json:"total_uah"`
	Tickets  int     `json:"tickets"`
	Replayed bool    `json:"replayed"`
	URL      string  `json:"url"`
}

func (t *Tools) BookTicket(ctx context.Context, userID int64, in BookTicketInput) (Order, error) {
	if userID <= 0 {
		return Order{}, &Error{Code: CodeNoAccount, Message: "sign in to book tickets"}
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) < keyMinLen || len(key) > keyMaxLen {
		return Order{}, &Error{Code: CodeInvalid, Message: fmt.Sprintf("idempotency_key must be %d-%d characters", keyMinLen, keyMaxLen)}
	}
	if in.Quantity < orders.MinQuantity || in.Quantity > orders.MaxQuantity {
		return Order{}, &Error{Code: CodeInvalid, Message: fmt.Sprintf("quantity must be between %d and %d", orders.MinQuantity, orders.MaxQuantity)}
	}
	p, err := t.events.PublicBySlug(ctx, strings.TrimSpace(in.EventSlug))
	if err != nil {
		return Order{}, toolError(err)
	}
	d, replayed, err := t.orders.Purchase(ctx, userID, orders.Input{
		EventID: p.Event.ID, TierID: in.TierID, Quantity: int32(in.Quantity), Key: BookingKey(userID, key),
	})
	if err != nil {
		return Order{}, toolError(err)
	}
	return Order{
		OrderID: d.ID, Status: d.Status, Event: d.EventTitle, Tier: d.TierName, Quantity: d.Quantity, TotalUAH: uah(d.Total),
		Tickets: len(d.Tickets), Replayed: replayed, URL: t.baseURL + "/orders/" + strconv.FormatInt(d.ID, 10),
	}, nil
}

func BookingKey(userID int64, key string) uuid.UUID {
	return uuid.NewSHA1(bookingNamespace, []byte(strconv.FormatInt(userID, 10)+":"+key))
}

type Booking struct {
	EventSlug string  `json:"event_slug"`
	EventName string  `json:"event"`
	StartsAt  string  `json:"starts_at"`
	Venue     string  `json:"venue"`
	TierID    int64   `json:"tier_id"`
	TierName  string  `json:"tier"`
	Quantity  int     `json:"quantity"`
	PriceUAH  float64 `json:"price_uah"`
	TotalUAH  float64 `json:"total_uah"`
	BuyURL    string  `json:"buy_url"`
}

func (t *Tools) CheckBooking(ctx context.Context, in BookTicketInput) (Booking, error) {
	if in.Quantity < orders.MinQuantity || in.Quantity > orders.MaxQuantity {
		return Booking{}, &Error{Code: CodeInvalid, Message: fmt.Sprintf("quantity must be between %d and %d", orders.MinQuantity, orders.MaxQuantity)}
	}
	e, err := t.GetEvent(ctx, GetEventInput{Slug: in.EventSlug})
	if err != nil {
		return Booking{}, err
	}
	i := slices.IndexFunc(e.Tiers, func(tier Tier) bool { return tier.TierID == in.TierID })
	switch {
	case i < 0:
		return Booking{}, &Error{Code: CodeInvalid, Message: "tier_id does not belong to this event, use get_event"}
	case !e.OnSale:
		return Booking{}, &Error{Code: CodeNotOnSale, Message: "event is not on sale"}
	case int(e.Tiers[i].Available) < in.Quantity:
		return Booking{}, &Error{Code: CodeSoldOut, Message: fmt.Sprintf("only %d tickets left in this tier", e.Tiers[i].Available)}
	}
	tier := e.Tiers[i]
	return Booking{
		EventSlug: e.Slug, EventName: e.Title, StartsAt: e.StartsAt, Venue: e.Venue, TierID: tier.TierID, TierName: tier.Name,
		Quantity: in.Quantity, PriceUAH: tier.PriceUAH, TotalUAH: tier.PriceUAH * float64(in.Quantity), BuyURL: e.URL + "/buy",
	}, nil
}

func toolError(err error) error {
	var evErr *events.ValidationError
	var ordErr *orders.ValidationError
	switch {
	case errors.As(err, &evErr):
		return &Error{Code: CodeInvalid, Message: "invalid " + fieldList(evErr.Fields)}
	case errors.As(err, &ordErr):
		return &Error{Code: CodeInvalid, Message: "invalid " + fieldList(ordErr.Fields)}
	case errors.Is(err, events.ErrNotFound), errors.Is(err, orders.ErrNotFound):
		return &Error{Code: CodeNotFound, Message: "event or tier not found"}
	case errors.Is(err, orders.ErrNotOnSale):
		return &Error{Code: CodeNotOnSale, Message: "event is not on sale"}
	case errors.Is(err, orders.ErrSoldOut):
		return &Error{Code: CodeSoldOut, Message: "not enough tickets left"}
	case errors.Is(err, orders.ErrKeyReused):
		return &Error{Code: CodeKeyReused, Message: "idempotency_key was already used for a different booking"}
	default:
		return fmt.Errorf("%s: %w", CodeInternal, err)
	}
}

var inputNames = map[string]string{
	events.FieldFrom:     "date_from",
	events.FieldTo:       "date_to",
	events.FieldMaxPrice: "max_price_uah",
	orders.FieldTier:     "tier_id",
}

func fieldList(fields map[string]string) string {
	parts := make([]string, 0, len(fields))
	for f, code := range fields {
		if name, ok := inputNames[f]; ok {
			f = name
		}
		parts = append(parts, f+" ("+code+")")
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

func (t *Tools) eventURL(slug string) string { return t.baseURL + "/events/" + slug }

func kyivTime(ts time.Time) string { return ts.In(kyiv).Format(time.RFC3339) }

func uah(kopecks int64) float64 { return float64(kopecks) / kopecksPerUAH }

func uahPtr(kopecks *int64) *float64 {
	if kopecks == nil {
		return nil
	}
	v := uah(*kopecks)
	return &v
}
