package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
)

const idempotencyHeader = "Idempotency-Key"

type ticketJSON struct {
	Code     string    `json:"code"`
	IssuedAt time.Time `json:"issuedAt"`
}

type orderJSON struct {
	ID            int64        `json:"id"`
	EventID       int64        `json:"eventId"`
	EventSlug     string       `json:"eventSlug"`
	EventTitle    string       `json:"eventTitle"`
	EventStartsAt time.Time    `json:"eventStartsAt"`
	VenueName     string       `json:"venueName"`
	VenueCity     string       `json:"venueCity"`
	TierID        int64        `json:"tierId"`
	TierName      string       `json:"tierName"`
	Quantity      int32        `json:"quantity"`
	UnitPrice     int64        `json:"unitPrice"`
	Total         int64        `json:"total"`
	Status        string       `json:"status"`
	CreatedAt     time.Time    `json:"createdAt"`
	Tickets       []ticketJSON `json:"tickets,omitempty"`
}

func orderFrom(d orders.Details) orderJSON {
	out := orderJSON{
		ID: d.ID, EventID: d.EventID, EventSlug: d.EventSlug, EventTitle: d.EventTitle, EventStartsAt: d.EventStartsAt,
		VenueName: d.VenueName, VenueCity: d.VenueCity, TierID: d.TierID, TierName: d.TierName, Quantity: d.Quantity,
		UnitPrice: d.UnitPrice, Total: d.Total, Status: d.Status, CreatedAt: d.CreatedAt,
	}
	for _, t := range d.Tickets {
		out.Tickets = append(out.Tickets, ticketJSON{Code: uuid.UUID(t.Code.Bytes).String(), IssuedAt: t.IssuedAt})
	}
	return out
}

type Orders struct {
	Svc *orders.Service
	Log *slog.Logger
}

func (c Orders) List(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.CurrentUser(r.Context())
	skip, limit, err := httpx.Paginate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	list, total, err := c.Svc.List(r.Context(), u.ID, limit, skip)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	items := make([]orderJSON, len(list))
	for i, d := range list {
		items[i] = orderFrom(d)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope(items, int64(total), skip, limit, r))
}

func (c Orders) Show(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.CurrentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "order not found", nil)
		return
	}
	d, err := c.Svc.Get(r.Context(), u.ID, id)
	if errors.Is(err, orders.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "order not found", nil)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, orderFrom(d))
}

func (c Orders) Create(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.CurrentUser(r.Context())
	key, err := uuid.Parse(r.Header.Get(idempotencyHeader))
	if err != nil || key == uuid.Nil {
		httpx.WriteError(w, http.StatusBadRequest, "Idempotency-Key header must be a UUID", nil)
		return
	}
	var body struct {
		TierID   int64 `json:"tierId"`
		Quantity int32 `json:"quantity"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	d, replay, err := c.Svc.Purchase(r.Context(), u.ID, orders.Input{TierID: body.TierID, Quantity: body.Quantity, Key: key})
	var verr *orders.ValidationError
	switch {
	case errors.As(err, &verr):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	case errors.Is(err, orders.ErrKeyReused):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "idempotency_key_reused", nil)
		return
	case errors.Is(err, orders.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "ticket tier not found", nil)
		return
	case errors.Is(err, orders.ErrNotOnSale):
		httpx.WriteError(w, http.StatusConflict, "not_on_sale", nil)
		return
	case errors.Is(err, orders.ErrSoldOut):
		httpx.WriteError(w, http.StatusConflict, "sold_out", nil)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	if replay {
		httpx.WriteJSON(w, http.StatusOK, orderFrom(d))
		return
	}
	c.Log.InfoContext(r.Context(), "order created via api", "order_id", d.ID, "user_id", u.ID, "tier_id", d.TierID, "quantity", d.Quantity, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusCreated, orderFrom(d))
}

func (c Orders) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "order api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}
