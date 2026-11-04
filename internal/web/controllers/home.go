package controllers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
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
	Cards []EventCard
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
		data.Cards = upcomingCards(rows)
	}
	h.View.Render(w, r, http.StatusOK, "pages/home", data)
}

func upcomingCards(rows []dbgen.ListUpcomingPublishedEventsRow) []EventCard {
	cards := make([]EventCard, 0, len(rows))
	for _, row := range rows {
		cards = append(cards, EventCard{
			Title: row.Event.Title, URL: "/events/" + row.Event.Slug, Venue: row.VenueName + ", " + row.VenueCity,
			StartsAt: row.Event.StartsAt, PosterURL: posterURL(row.Event.PosterKey),
		})
	}
	return cards
}
