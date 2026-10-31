package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type mapEventJSON struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	StartsAt  time.Time `json:"starts_at"`
	Genre     string    `json:"genre"`
	PriceFrom *int64    `json:"price_from"`
}

type mapVenueJSON struct {
	ID      int64          `json:"id"`
	Name    string         `json:"name"`
	City    string         `json:"city"`
	Address string         `json:"address"`
	Lat     float64        `json:"lat"`
	Lng     float64        `json:"lng"`
	Events  []mapEventJSON `json:"events"`
	More    int            `json:"more"`
}

type mapJSON struct {
	Venues []mapVenueJSON `json:"venues"`
}

type VenueMap struct {
	View *view.Renderer
	Svc  *events.Service
	Log  *slog.Logger
}

func mapQueryFrom(r *http.Request) events.MapQuery {
	q := r.URL.Query()
	return events.MapQuery{From: q.Get(events.FieldFrom), To: q.Get(events.FieldTo), Genre: q.Get(events.FieldGenre), MaxPrice: q.Get(events.FieldMaxPrice)}
}

type mapEventView struct {
	Title    string
	Slug     string
	StartsAt time.Time
	Genre    string
	Price    int64
	HasPrice bool
}

type mapVenueView struct {
	Name    string
	City    string
	Address string
	Events  []mapEventView
	More    int
}

type venueMapPage struct {
	Query    events.MapQuery
	Genres   []eventGenreOption
	Errors   map[string]string
	Venues   []mapVenueView
	Filtered bool
}

func mapVenueViews(found []events.MapVenue) []mapVenueView {
	out := make([]mapVenueView, 0, len(found))
	for _, v := range found {
		mv := mapVenueView{Name: v.Name, City: v.City, Address: v.Address, More: v.More}
		for _, e := range v.Events {
			ev := mapEventView{Title: e.Title, Slug: e.Slug, StartsAt: e.StartsAt, Genre: e.Genre}
			if e.PriceFrom != nil {
				ev.Price, ev.HasPrice = *e.PriceFrom, true
			}
			mv.Events = append(mv.Events, ev)
		}
		out = append(out, mv)
	}
	return out
}

func mapErrorKeys(fields map[string]string) map[string]string {
	keys := make(map[string]string, len(fields))
	for field, code := range fields {
		keys[field] = "map.error." + field + "_" + code
	}
	return keys
}

func (c VenueMap) Page(w http.ResponseWriter, r *http.Request) {
	q := mapQueryFrom(r)
	data := venueMapPage{Query: q, Genres: genreOptions(q.Genre), Filtered: q != events.MapQuery{}}
	found, err := c.Svc.Map(r.Context(), time.Now(), q)
	var verr *events.ValidationError
	switch {
	case errors.As(err, &verr):
		data.Errors = mapErrorKeys(verr.Fields)
		c.View.Render(w, r, http.StatusBadRequest, "pages/venue_map", data)
		return
	case err != nil:
		c.Log.ErrorContext(r.Context(), "venue map query failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		c.View.ServerError(w, r)
		return
	}
	data.Venues = mapVenueViews(found)
	c.View.Render(w, r, http.StatusOK, "pages/venue_map", data)
}

func (c VenueMap) JSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	found, err := c.Svc.Map(r.Context(), time.Now(), mapQueryFrom(r))
	var verr *events.ValidationError
	if errors.As(err, &verr) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid map filters", verr.Fields)
		return
	}
	if err != nil {
		c.Log.ErrorContext(r.Context(), "venue map query failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	lang := i18n.FromContext(r.Context())
	out := mapJSON{Venues: make([]mapVenueJSON, 0, len(found))}
	for _, v := range found {
		mv := mapVenueJSON{ID: v.ID, Name: v.Name, City: v.City, Address: v.Address, Lat: v.Lat, Lng: v.Lng, More: v.More, Events: make([]mapEventJSON, 0, len(v.Events))}
		for _, e := range v.Events {
			mv.Events = append(mv.Events, mapEventJSON{
				Title: e.Title, URL: i18n.Path(lang, "/events/"+e.Slug), StartsAt: e.StartsAt, Genre: e.Genre, PriceFrom: e.PriceFrom,
			})
		}
		out.Venues = append(out.Venues, mv)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
