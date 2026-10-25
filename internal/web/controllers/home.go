package controllers

import (
	"net/http"

	"github.com/KotunOleg/Tixo/internal/web/view"
)

type EventCard struct {
	Title     string
	URL       string
	Venue     string
	StartsAt  string
	PosterURL string
}

type homeData struct {
	Upcoming []EventCard
}

type Home struct {
	View *view.Renderer
}

func (h Home) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.View.Render(w, r, http.StatusOK, "pages/home", homeData{})
}
