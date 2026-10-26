package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/cache"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
)

const allEventsLimit = 1 << 20

type eventJSON struct {
	ID          int64     `json:"id"`
	OrganizerID int64     `json:"organizerId"`
	VenueID     int64     `json:"venueId"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartsAt    time.Time `json:"startsAt"`
	EndsAt      time.Time `json:"endsAt"`
	Genre       string    `json:"genre"`
	PriceFrom   *int64    `json:"priceFrom"`
	PosterKey   string    `json:"posterKey"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func eventFrom(e dbgen.Event) eventJSON {
	var priceFrom *int64
	if e.PriceFrom.Valid {
		priceFrom = &e.PriceFrom.Int64
	}
	return eventJSON{
		ID: e.ID, OrganizerID: e.OrganizerID, VenueID: e.VenueID, Slug: e.Slug, Title: e.Title, Description: e.Description,
		StartsAt: e.StartsAt, EndsAt: e.EndsAt, Genre: e.Genre, PriceFrom: priceFrom, PosterKey: e.PosterKey.String,
		Status: e.Status, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
}

type eventBody struct {
	VenueID     int64     `json:"venueId"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartsAt    time.Time `json:"startsAt"`
	EndsAt      time.Time `json:"endsAt"`
	Genre       string    `json:"genre"`
	PriceFrom   *int64    `json:"priceFrom"`
	PosterKey   string    `json:"posterKey"`
}

func (b eventBody) toInput() events.Input {
	return events.Input{
		VenueID: b.VenueID, Slug: b.Slug, Title: b.Title, Description: b.Description,
		StartsAt: b.StartsAt, EndsAt: b.EndsAt, Genre: b.Genre, PriceFrom: b.PriceFrom, PosterKey: b.PosterKey,
	}
}

type Events struct {
	Svc   *events.Service
	Cache *cache.Store
	Log   *slog.Logger
}

func (c Events) eventsCacheNS(orgID int64) string {
	return "cache:org:" + strconv.FormatInt(orgID, 10) + ":events"
}

func (c Events) List(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	list, hit, err := cache.Fetch(r.Context(), c.Cache, c.eventsCacheNS(m.Organizer.ID), "list", cacheTTL,
		func(ctx context.Context) ([]dbgen.Event, error) {
			return c.Svc.List(ctx, m.Organizer.ID, allEventsLimit, 0)
		})
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	skip, limit, err := httpx.Paginate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	items := make([]eventJSON, len(list))
	for i, e := range list {
		items[i] = eventFrom(e)
	}
	w.Header().Set("X-Cache", cacheStatus(hit))
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope(pageSlice(items, skip, limit), int64(len(items)), skip, limit, r))
}

func (c Events) Show(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	e, hit, err := cache.Fetch(r.Context(), c.Cache, c.eventsCacheNS(m.Organizer.ID), strconv.FormatInt(id, 10), cacheTTL,
		func(ctx context.Context) (dbgen.Event, error) {
			return c.Svc.Get(ctx, m.Organizer.ID, id)
		})
	if errors.Is(err, events.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	w.Header().Set("X-Cache", cacheStatus(hit))
	httpx.WriteJSON(w, http.StatusOK, eventFrom(e))
}

func (c Events) Create(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	var body eventBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	e, err := c.Svc.Create(r.Context(), m.Organizer.ID, body.toInput())
	var verr *events.ValidationError
	if errors.As(err, &verr) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "event created via api", "event_id", e.ID, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusCreated, eventFrom(e))
}

func (c Events) Update(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	var body eventBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	e, err := c.Svc.Update(r.Context(), m.Organizer.ID, id, body.toInput())
	var verr *events.ValidationError
	switch {
	case errors.Is(err, events.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Cache.Bump(r.Context(), c.eventsCacheNS(m.Organizer.ID))
	c.Log.InfoContext(r.Context(), "event updated via api", "event_id", e.ID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusOK, eventFrom(e))
}

func (c Events) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	e, err := c.Svc.SetStatus(r.Context(), m.Organizer.ID, id, body.Status)
	switch {
	case errors.Is(err, events.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	case errors.Is(err, events.ErrBadStatus):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "invalid status", map[string]string{"status": "invalid"})
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Cache.Bump(r.Context(), c.eventsCacheNS(m.Organizer.ID))
	c.Log.InfoContext(r.Context(), "event status changed via api", "event_id", e.ID, "status", e.Status, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusOK, eventFrom(e))
}

func (c Events) Delete(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	err = c.Svc.Delete(r.Context(), m.Organizer.ID, id)
	switch {
	case errors.Is(err, events.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	case errors.Is(err, events.ErrHasTickets):
		httpx.WriteError(w, http.StatusConflict, "event has ticket tiers and cannot be deleted", nil)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Cache.Bump(r.Context(), c.eventsCacheNS(m.Organizer.ID))
	c.Log.InfoContext(r.Context(), "event deleted via api", "event_id", id, "request_id", middleware.GetReqID(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

func (c Events) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "event api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}
