package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

const (
	ticketsPageSize = 20
	ticketsMaxPage  = 100_000
)

type orderView struct {
	ID         int64
	EventSlug  string
	EventTitle string
	StartsAt   time.Time
	Venue      string
	TierName   string
	Quantity   int
	UnitPrice  int64
	Total      int64
	Past       bool
	Cancelled  bool
	Codes      []string
}

type ticketsData struct {
	Orders   []orderView
	Page     int
	PrevPage int
	NextPage int
}

type Orders struct {
	View *view.Renderer
	Svc  *orders.Service
	Log  *slog.Logger
}

func (c Orders) Show(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.CurrentUser(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	d, err := c.Svc.Get(r.Context(), u.ID, id)
	if errors.Is(err, orders.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.View.Render(w, r, http.StatusOK, "pages/order_show", orderViewFrom(d, time.Now()))
}

func (c Orders) List(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.CurrentUser(r.Context())
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	page = min(page, ticketsMaxPage)
	list, total, err := c.Svc.List(r.Context(), u.ID, ticketsPageSize, int32((page-1)*ticketsPageSize))
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	if len(list) == 0 && total > 0 {
		redirectLocal(w, ticketsPageURL(r, (int(total)+ticketsPageSize-1)/ticketsPageSize), http.StatusFound)
		return
	}
	data := ticketsData{Page: page}
	now := time.Now()
	for _, d := range list {
		data.Orders = append(data.Orders, orderViewFrom(d, now))
	}
	if page > 1 {
		data.PrevPage = page - 1
	}
	if page*ticketsPageSize < int(total) {
		data.NextPage = page + 1
	}
	c.View.Render(w, r, http.StatusOK, "pages/tickets", data)
}

func ticketsPageURL(r *http.Request, page int) string {
	target := i18n.Path(i18n.FromContext(r.Context()), "/tickets")
	if page > 1 {
		target += "?page=" + strconv.Itoa(page)
	}
	return target
}

func orderViewFrom(d orders.Details, now time.Time) orderView {
	v := orderView{
		ID: d.ID, EventSlug: d.EventSlug, EventTitle: d.EventTitle, StartsAt: d.EventStartsAt,
		Venue: d.VenueName + ", " + d.VenueCity, TierName: d.TierName, Quantity: int(d.Quantity),
		UnitPrice: d.UnitPrice, Total: d.Total, Past: !d.EventStartsAt.After(now), Cancelled: d.EventStatus == events.StatusCancelled,
	}
	for _, t := range d.Tickets {
		v.Codes = append(v.Codes, uuid.UUID(t.Code.Bytes).String())
	}
	return v
}

func (c Orders) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "orders page failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}
