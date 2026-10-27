package controllers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type EventCard struct {
	Title     string
	URL       string
	Venue     string
	StartsAt  time.Time
	PosterURL string
}

type homeData struct {
	Upcoming []EventCard
}

type Home struct {
	View   *view.Renderer
	Events *events.Service
	Log    *slog.Logger
}

func (h Home) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var data homeData
	if h.Events != nil {
		rows, err := h.Events.Upcoming(r.Context(), time.Now(), homeUpcomingLimit)
		if err != nil {
			h.Log.ErrorContext(r.Context(), "home upcoming events failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
			h.View.ServerError(w, r)
			return
		}
		for _, row := range rows {
			data.Upcoming = append(data.Upcoming, EventCard{
				Title: row.Event.Title, URL: "/events/" + row.Event.Slug, Venue: row.VenueName + ", " + row.VenueCity,
				StartsAt: row.Event.StartsAt, PosterURL: posterURL(row.Event.PosterKey),
			})
		}
	}
	h.View.Render(w, r, http.StatusOK, "pages/home", data)
}
