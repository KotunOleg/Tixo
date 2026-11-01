package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type mapEventJSON struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	StartsAt  time.Time `json:"starts_at"`
	Genre     string    `json:"genre"`
	PriceFrom *int64    `json:"price_from"`
	When      string    `json:"when"`
	Facts     string    `json:"facts"`
}

type mapVenueJSON struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	City      string         `json:"city"`
	Address   string         `json:"address"`
	Place     string         `json:"place"`
	Lat       float64        `json:"lat"`
	Lng       float64        `json:"lng"`
	Events    []mapEventJSON `json:"events"`
	More      int            `json:"more"`
	MoreLabel string         `json:"more_label"`
}

type mapJSON struct {
	Summary string         `json:"summary"`
	Venues  []mapVenueJSON `json:"venues"`
}

type mapTexts struct {
	EmptyTitle   string            `json:"empty_title"`
	EmptyText    string            `json:"empty_text"`
	Reset        string            `json:"reset"`
	ResetURL     string            `json:"reset_url"`
	NetworkError string            `json:"network_error"`
	ErrorsTitle  string            `json:"errors_title"`
	Errors       map[string]string `json:"errors"`
}

var mapErrorCodes = map[string][]string{
	events.FieldFrom:     {events.CodeFormat},
	events.FieldTo:       {events.CodeFormat, events.CodeRange},
	events.FieldGenre:    {events.CodeInvalid},
	events.FieldMaxPrice: {events.CodeFormat, events.CodeRange},
}

func mapErrorMessageID(field, code string) string {
	return "map.error." + field + "_" + code
}

type VenueMap struct {
	View *view.Renderer
	Svc  *events.Service
	Log  *slog.Logger
}

type venueMapPage struct {
	Query    events.MapQuery
	Genres   []eventGenreOption
	Errors   map[string]string
	Map      mapJSON
	Texts    mapTexts
	Filtered bool
}

func mapQueryFrom(r *http.Request) events.MapQuery {
	q := r.URL.Query()
	return events.MapQuery{From: q.Get(events.FieldFrom), To: q.Get(events.FieldTo), Genre: q.Get(events.FieldGenre), MaxPrice: q.Get(events.FieldMaxPrice)}
}

func buildMapJSON(p *view.Page, found []events.MapVenue) (mapJSON, error) {
	summary, err := p.N("map.results.count", len(found))
	if err != nil {
		return mapJSON{}, err
	}
	out := mapJSON{Summary: summary, Venues: make([]mapVenueJSON, 0, len(found))}
	for _, v := range found {
		mv := mapVenueJSON{
			ID: v.ID, Name: v.Name, City: v.City, Address: v.Address, Place: v.City, Lat: v.Lat, Lng: v.Lng,
			More: v.More, Events: make([]mapEventJSON, 0, len(v.Events)),
		}
		if v.Address != "" {
			mv.Place += " · " + v.Address
		}
		if v.More > 0 {
			if mv.MoreLabel, err = p.N("map.results.more", v.More); err != nil {
				return mapJSON{}, err
			}
		}
		for _, e := range v.Events {
			ev, err := buildMapEvent(p, e)
			if err != nil {
				return mapJSON{}, err
			}
			mv.Events = append(mv.Events, ev)
		}
		out.Venues = append(out.Venues, mv)
	}
	return out, nil
}

func buildMapEvent(p *view.Page, e events.MapEvent) (mapEventJSON, error) {
	ev := mapEventJSON{Title: e.Title, URL: p.URL("/events/" + e.Slug), StartsAt: e.StartsAt, Genre: e.Genre, PriceFrom: e.PriceFrom}
	var err error
	if ev.When, err = p.DateTime(e.StartsAt); err != nil {
		return ev, err
	}
	if ev.Facts, err = p.T("genre." + e.Genre); err != nil {
		return ev, err
	}
	if e.PriceFrom == nil {
		return ev, nil
	}
	price, err := p.Money(*e.PriceFrom)
	if err != nil {
		return ev, err
	}
	if *e.PriceFrom > 0 {
		if price, err = p.T("map.results.price_from", "Price", price); err != nil {
			return ev, err
		}
	}
	ev.Facts += " · " + price
	return ev, nil
}

func buildMapTexts(p *view.Page) (mapTexts, error) {
	t := mapTexts{ResetURL: p.URL("/map"), Errors: map[string]string{}}
	for _, f := range []struct {
		dst *string
		id  string
	}{
		{&t.EmptyTitle, "map.empty.title"}, {&t.EmptyText, "map.empty.text"}, {&t.Reset, "map.filters.reset"}, {&t.NetworkError, "map.network_error"}, {&t.ErrorsTitle, "map.errors_title"},
	} {
		var err error
		if *f.dst, err = p.T(f.id); err != nil {
			return t, err
		}
	}
	for field, codes := range mapErrorCodes {
		for _, code := range codes {
			msg, err := p.T(mapErrorMessageID(field, code))
			if err != nil {
				return t, err
			}
			t.Errors[field+"_"+code] = msg
		}
	}
	return t, nil
}

func (c VenueMap) Page(w http.ResponseWriter, r *http.Request) {
	q := mapQueryFrom(r)
	p := c.View.PageFor(r)
	data := venueMapPage{Query: q, Genres: genreOptions(q.Genre), Filtered: q != events.MapQuery{}, Map: mapJSON{Venues: []mapVenueJSON{}}}
	texts, err := buildMapTexts(p)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	data.Texts = texts
	found, err := c.Svc.Map(r.Context(), time.Now(), q)
	var verr *events.ValidationError
	switch {
	case errors.As(err, &verr):
		data.Errors = make(map[string]string, len(verr.Fields))
		for field, code := range verr.Fields {
			data.Errors[field] = mapErrorMessageID(field, code)
		}
		c.View.Render(w, r, http.StatusBadRequest, "pages/venue_map", data)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	if data.Map, err = buildMapJSON(p, found); err != nil {
		c.serverError(w, r, err)
		return
	}
	c.View.Render(w, r, http.StatusOK, "pages/venue_map", data)
}

func (c VenueMap) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "venue map failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}

func (c VenueMap) JSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	found, err := c.Svc.Map(r.Context(), time.Now(), mapQueryFrom(r))
	var verr *events.ValidationError
	if errors.As(err, &verr) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid map filters", verr.Fields)
		return
	}
	var out mapJSON
	if err == nil {
		out, err = buildMapJSON(c.View.PageFor(r), found)
	}
	if err != nil {
		c.Log.ErrorContext(r.Context(), "venue map json failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
