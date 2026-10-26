package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/artists"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/lineup"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type Deps struct {
	Organizers *organizers.Service
	Venues     *venues.Service
	Events     *events.Service
	Tiers      *tiers.Service
	Lineup     *lineup.Service
	Artists    *artists.Service
	APITokens  *apitokens.Service
	Logger     *slog.Logger
}

func Mount(r chi.Router, d Deps) {
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(RequireAPIUser(d.APITokens, d.Logger))

		o := Organizers{Svc: d.Organizers, Log: d.Logger}
		r.Get("/organizers", o.List)

		v := Venues{Svc: d.Venues, Log: d.Logger}
		r.Route("/organizers/{slug}", func(r chi.Router) {
			r.Use(requireMember(d.Organizers))
			r.Get("/", o.Show)
			r.Patch("/", o.Update)

			r.Get("/venues", v.List)
			r.Post("/venues", v.Create)
			r.Get("/venues/{id}", v.Show)
			r.Patch("/venues/{id}", v.Update)
			r.Delete("/venues/{id}", v.Delete)
		})
	})
}
