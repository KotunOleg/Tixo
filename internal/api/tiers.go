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
	"github.com/KotunOleg/Tixo/internal/tiers"
)

type tierJSON struct {
	ID        int64  `json:"id"`
	EventID   int64  `json:"eventId"`
	Name      string `json:"name"`
	Price     int64  `json:"price"`
	Quantity  int32  `json:"quantity"`
	Available int32  `json:"available"`
}

func tierFrom(t dbgen.TicketTier) tierJSON {
	return tierJSON{ID: t.ID, EventID: t.EventID, Name: t.Name, Price: t.Price, Quantity: t.Quantity, Available: t.Available}
}

type tierBody struct {
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Quantity int32  `json:"quantity"`
}

func (b tierBody) toInput() tiers.Input {
	return tiers.Input{Name: b.Name, Price: b.Price, Quantity: b.Quantity}
}

type Tiers struct {
	Svc *tiers.Service
	Log *slog.Logger
}

func (c Tiers) List(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	list, err := c.Svc.List(r.Context(), m.Organizer.ID, eventID)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	skip, limit, err := httpx.Paginate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	items := make([]tierJSON, len(list))
	for i, t := range list {
		items[i] = tierFrom(t)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope(pageSlice(items, skip, limit), int64(len(items)), skip, limit, r))
}

func (c Tiers) Create(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	var body tierBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	t, err := c.Svc.Create(r.Context(), m.Organizer.ID, eventID, body.toInput())
	var verr *tiers.ValidationError
	switch {
	case errors.Is(err, tiers.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "ticket tier created via api", "tier_id", t.ID, "event_id", eventID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusCreated, tierFrom(t))
}

func (c Tiers) Update(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "tierID"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "ticket tier not found", nil)
		return
	}
	var body tierBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	t, err := c.Svc.Update(r.Context(), m.Organizer.ID, id, body.toInput())
	var verr *tiers.ValidationError
	switch {
	case errors.Is(err, tiers.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "ticket tier not found", nil)
		return
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "ticket tier updated via api", "tier_id", t.ID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusOK, tierFrom(t))
}

func (c Tiers) Delete(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "tierID"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "ticket tier not found", nil)
		return
	}
	err = c.Svc.Delete(r.Context(), m.Organizer.ID, id)
	switch {
	case errors.Is(err, tiers.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "ticket tier not found", nil)
		return
	case errors.Is(err, tiers.ErrSold):
		httpx.WriteError(w, http.StatusConflict, "ticket tier has sold tickets and cannot be deleted", nil)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "ticket tier deleted via api", "tier_id", id, "request_id", middleware.GetReqID(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

func (c Tiers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "tier api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}
