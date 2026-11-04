package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type eventsSearchData struct {
	Query    string
	Cards    []EventCard
	Searched bool
	Errors   map[string]string
}

type EventsSearch struct {
	View   *view.Renderer
	Events *events.Service
	Log    *slog.Logger
}

func (c EventsSearch) Show(w http.ResponseWriter, r *http.Request) {
	data := eventsSearchData{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if c.Events == nil {
		c.View.Render(w, r, http.StatusOK, "pages/events_search", data)
		return
	}
	now := time.Now()
	if data.Query == "" {
		rows, err := c.Events.Upcoming(r.Context(), now, homeUpcomingLimit)
		if err != nil {
			c.serverError(w, r, err)
			return
		}
		for _, row := range rows {
			data.Cards = append(data.Cards, EventCard{
				Title: row.Event.Title, URL: "/events/" + row.Event.Slug, Venue: row.VenueName + ", " + row.VenueCity,
				StartsAt: row.Event.StartsAt, PosterURL: posterURL(row.Event.PosterKey),
			})
		}
		c.View.Render(w, r, http.StatusOK, "pages/events_search", data)
		return
	}
	data.Searched = true
	found, err := c.Events.Search(r.Context(), now, events.SearchQuery{Text: data.Query})
	var verr *events.ValidationError
	switch {
	case errors.As(err, &verr):
		data.Errors = map[string]string{events.FieldQuery: "events.search.error_" + verr.Fields[events.FieldQuery]}
		c.View.Render(w, r, http.StatusBadRequest, "pages/events_search", data)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	for _, res := range found {
		data.Cards = append(data.Cards, EventCard{
			Title: res.Title, URL: "/events/" + res.Slug, Venue: res.VenueName + ", " + res.VenueCity, StartsAt: res.StartsAt,
		})
	}
	c.View.Render(w, r, http.StatusOK, "pages/events_search", data)
}

func (c EventsSearch) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "events search failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}
