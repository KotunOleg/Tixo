package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type venueJSON struct {
	ID          int64   `json:"id"`
	OrganizerID int64   `json:"organizerId"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	City        string  `json:"city"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	Capacity    int32   `json:"capacity"`
	Shared      bool    `json:"shared"`
}

func venueFrom(v dbgen.Venue) venueJSON {
	return venueJSON{
		ID: v.ID, OrganizerID: v.OrganizerID.Int64, Name: v.Name, Address: v.Address, City: v.City,
		Lat: v.Lat, Lng: v.Lng, Capacity: v.Capacity, Shared: !v.OrganizerID.Valid,
	}
}

type venueBody struct {
	Name     string  `json:"name"`
	Address  string  `json:"address"`
	City     string  `json:"city"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Capacity int32   `json:"capacity"`
}

func (b venueBody) toInput() venues.Input {
	return venues.Input{Name: b.Name, Address: b.Address, City: b.City, Lat: b.Lat, Lng: b.Lng, Capacity: b.Capacity}
}

type Venues struct {
	Svc *venues.Service
	Log *slog.Logger
}

func (c Venues) List(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	list, err := c.Svc.List(r.Context(), m.Organizer.ID)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	skip, limit, err := httpx.Paginate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	items := make([]venueJSON, len(list))
	for i, v := range list {
		items[i] = venueFrom(v)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope(pageSlice(items, skip, limit), int64(len(items)), skip, limit, r))
}

func (c Venues) Show(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "venue not found", nil)
		return
	}
	v, err := c.Svc.Get(r.Context(), m.Organizer.ID, id)
	if errors.Is(err, venues.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "venue not found", nil)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, venueFrom(v))
}

func (c Venues) Create(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	var body venueBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	v, err := c.Svc.Create(r.Context(), m.Organizer.ID, body.toInput())
	var verr *venues.ValidationError
	if errors.As(err, &verr) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "venue created via api", "venue_id", v.ID, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusCreated, venueFrom(v))
}

func (c Venues) Update(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "venue not found", nil)
		return
	}
	var body venueBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	v, err := c.Svc.Update(r.Context(), m.Organizer.ID, id, body.toInput())
	var verr *venues.ValidationError
	switch {
	case errors.Is(err, venues.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "venue not found", nil)
		return
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "venue updated via api", "venue_id", v.ID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusOK, venueFrom(v))
}

func (c Venues) Delete(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "venue not found", nil)
		return
	}
	err = c.Svc.Delete(r.Context(), m.Organizer.ID, id)
	switch {
	case errors.Is(err, venues.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "venue not found", nil)
		return
	case errors.Is(err, venues.ErrHasEvents):
		httpx.WriteError(w, http.StatusConflict, "venue has events and cannot be deleted", nil)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "venue deleted via api", "venue_id", id, "request_id", middleware.GetReqID(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

func (c Venues) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "venue api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}
