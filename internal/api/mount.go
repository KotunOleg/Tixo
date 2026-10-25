package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/analytics"
	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/artists"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/lineup"
	"github.com/KotunOleg/Tixo/internal/orders"
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
	Orders     *orders.Service
	Analytics  *analytics.Service
	APITokens  *apitokens.Service
	Logger     *slog.Logger
}

func Mount(r chi.Router, d Deps) {
	r.Get("/api/docs", ServeDocs)
	r.Get("/api/openapi.yaml", ServeOpenAPISpec)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(RequireAPIUser(d.APITokens, d.Logger))

		o := Organizers{Svc: d.Organizers, Log: d.Logger}
		r.Get("/organizers", o.List)

		a := Artists{Svc: d.Artists, Log: d.Logger}
		r.Get("/artists", a.List)
		r.Post("/artists", a.Create)
		r.Get("/artists/{id}", a.Show)

		ord := Orders{Svc: d.Orders, Log: d.Logger}
		r.Get("/orders", ord.List)
		r.Post("/orders", ord.Create)
		r.Get("/orders/{id}", ord.Show)

		v := Venues{Svc: d.Venues, Log: d.Logger}
		r.Route("/organizers/{slug}", func(r chi.Router) {
			r.Use(requireMember(d.Organizers))
			r.Get("/", o.Show)
			r.Patch("/", o.Update)

			an := Analytics{Svc: d.Analytics, Log: d.Logger}
			r.Get("/analytics", an.Show)

			r.Get("/venues", v.List)
			r.Post("/venues", v.Create)
			r.Get("/venues/{id}", v.Show)
			r.Patch("/venues/{id}", v.Update)
			r.Delete("/venues/{id}", v.Delete)

			e := Events{Svc: d.Events, Log: d.Logger}
			r.Get("/events", e.List)
			r.Post("/events", e.Create)
			r.Get("/events/{id}", e.Show)
			r.Patch("/events/{id}", e.Update)
			r.Post("/events/{id}/status", e.UpdateStatus)
			r.Delete("/events/{id}", e.Delete)

			ti := Tiers{Svc: d.Tiers, Log: d.Logger}
			r.Get("/events/{id}/tiers", ti.List)
			r.Post("/events/{id}/tiers", ti.Create)
			r.Patch("/events/{id}/tiers/{tierID}", ti.Update)
			r.Delete("/events/{id}/tiers/{tierID}", ti.Delete)

			lu := Lineup{Svc: d.Lineup, Log: d.Logger}
			r.Get("/events/{id}/lineup", lu.List)
			r.Post("/events/{id}/lineup", lu.Add)
			r.Delete("/events/{id}/lineup/{artistID}", lu.Remove)
		})
	})
}
