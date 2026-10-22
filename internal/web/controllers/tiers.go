package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type Tiers struct {
	View     *view.Renderer
	Sessions *scs.SessionManager
	Svc      *tiers.Service
	Log      *slog.Logger
}

func (c Tiers) Create(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		c.View.NotFound(w, r)
		return
	}
	in, ok := c.parseTierForm(r)
	if !ok {
		c.back(w, r, m.Organizer.Slug, eventID, "tier.form.error.format")
		return
	}
	t, err := c.Svc.Create(r.Context(), m.Organizer.ID, eventID, in)
	var verr *tiers.ValidationError
	switch {
	case errors.Is(err, tiers.ErrNotFound):
		c.View.NotFound(w, r)
		return
	case errors.As(err, &verr):
		c.back(w, r, m.Organizer.Slug, eventID, firstFieldError(verr))
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "ticket tier created", "tier_id", t.ID, "event_id", eventID, "request_id", middleware.GetReqID(r.Context()))
	c.back(w, r, m.Organizer.Slug, eventID, "tier.create.saved")
}

func (c Tiers) Update(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	tierID, err := strconv.ParseInt(chi.URLParam(r, "tierID"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		c.View.NotFound(w, r)
		return
	}
	in, ok := c.parseTierForm(r)
	if !ok {
		c.back(w, r, m.Organizer.Slug, eventID, "tier.form.error.format")
		return
	}
	t, err := c.Svc.Update(r.Context(), m.Organizer.ID, tierID, in)
	var verr *tiers.ValidationError
	switch {
	case errors.Is(err, tiers.ErrNotFound):
		c.View.NotFound(w, r)
		return
	case errors.As(err, &verr):
		c.back(w, r, m.Organizer.Slug, eventID, firstFieldError(verr))
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "ticket tier updated", "tier_id", t.ID, "event_id", eventID, "request_id", middleware.GetReqID(r.Context()))
	c.back(w, r, m.Organizer.Slug, eventID, "tier.update.saved")
}

func (c Tiers) Delete(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	tierID, err := strconv.ParseInt(chi.URLParam(r, "tierID"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	err = c.Svc.Delete(r.Context(), m.Organizer.ID, tierID)
	switch {
	case errors.Is(err, tiers.ErrNotFound):
		c.View.NotFound(w, r)
		return
	case errors.Is(err, tiers.ErrSold):
		c.back(w, r, m.Organizer.Slug, eventID, "tier.delete.sold")
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "ticket tier deleted", "tier_id", tierID, "event_id", eventID, "request_id", middleware.GetReqID(r.Context()))
	c.back(w, r, m.Organizer.Slug, eventID, "tier.delete.saved")
}

func (c Tiers) parseTierForm(r *http.Request) (tiers.Input, bool) {
	price, priceErr := parsePrice(strings.TrimSpace(r.PostForm.Get("price")))
	quantity, qtyErr := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("quantity")), 10, 32)
	if priceErr != nil || price == nil || qtyErr != nil {
		return tiers.Input{}, false
	}
	return tiers.Input{Name: r.PostForm.Get("name"), Price: *price, Quantity: int32(quantity)}, true
}

func (c Tiers) back(w http.ResponseWriter, r *http.Request, slug string, eventID int64, flash string) {
	auth.SetFlash(r.Context(), c.Sessions, flash)
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+slug+"/events/"+strconv.FormatInt(eventID, 10)), http.StatusSeeOther)
}

func (c Tiers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "ticket tier handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}

func firstFieldError(verr *tiers.ValidationError) string {
	fields := make([]string, 0, len(verr.Fields))
	for f := range verr.Fields {
		fields = append(fields, f)
	}
	slices.Sort(fields)
	field := fields[0]
	return "tier.form.error." + field + "_" + verr.Fields[field]
}
