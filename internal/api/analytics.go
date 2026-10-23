package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/analytics"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
)

const (
	dateLayout        = "2006-01-02"
	errFeatureMissing = "feature_not_enabled"
	rowKindEvent      = "event"
	rowKindOthers     = "others"
)

type analyticsTotalsJSON struct {
	Tickets         int64 `json:"tickets"`
	Revenue         int64 `json:"revenue"`
	PublishedEvents int64 `json:"published_events"`
}

type analyticsDayJSON struct {
	Date    string `json:"date"`
	Tickets int64  `json:"tickets"`
	Revenue int64  `json:"revenue"`
}

type analyticsEventJSON struct {
	Kind        string `json:"kind"`
	ID          int64  `json:"id,omitempty"`
	Slug        string `json:"slug,omitempty"`
	Title       string `json:"title,omitempty"`
	OthersCount int    `json:"others_count,omitempty"`
	Tickets     int64  `json:"tickets"`
	Revenue     int64  `json:"revenue"`
}

type analyticsGenreJSON struct {
	Genre  string `json:"genre"`
	Events int64  `json:"events"`
}

type analyticsJSON struct {
	Days    int                  `json:"days"`
	From    string               `json:"from"`
	To      string               `json:"to"`
	Totals  analyticsTotalsJSON  `json:"totals"`
	ByDay   []analyticsDayJSON   `json:"by_day"`
	ByEvent []analyticsEventJSON `json:"by_event"`
	ByGenre []analyticsGenreJSON `json:"by_genre"`
}

func analyticsFrom(r analytics.Report) analyticsJSON {
	out := analyticsJSON{
		Days: r.Days, From: r.From.Format(dateLayout), To: r.To.Format(dateLayout),
		Totals:  analyticsTotalsJSON{Tickets: r.Tickets, Revenue: r.Revenue, PublishedEvents: r.PublishedEvents},
		ByDay:   make([]analyticsDayJSON, len(r.ByDay)),
		ByEvent: make([]analyticsEventJSON, len(r.ByEvent)),
		ByGenre: make([]analyticsGenreJSON, len(r.ByGenre)),
	}
	for i, d := range r.ByDay {
		out.ByDay[i] = analyticsDayJSON{Date: d.Date.Format(dateLayout), Tickets: d.Tickets, Revenue: d.Revenue}
	}
	for i, e := range r.ByEvent {
		row := analyticsEventJSON{Kind: rowKindEvent, ID: e.ID, Slug: e.Slug, Title: e.Title, Tickets: e.Tickets, Revenue: e.Revenue}
		if e.IsOthers() {
			row = analyticsEventJSON{Kind: rowKindOthers, OthersCount: e.OthersCount, Tickets: e.Tickets, Revenue: e.Revenue}
		}
		out.ByEvent[i] = row
	}
	for i, g := range r.ByGenre {
		out.ByGenre[i] = analyticsGenreJSON{Genre: g.Genre, Events: g.Events}
	}
	return out
}

type Analytics struct {
	Svc *analytics.Service
	Log *slog.Logger
}

func (a Analytics) Show(w http.ResponseWriter, r *http.Request) {
	days := analytics.DefaultDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, analytics.ErrInvalidDays.Error(), nil)
			return
		}
		days = n
	}
	report, err := a.Svc.Report(r.Context(), membershipFrom(r), days)
	switch {
	case errors.Is(err, analytics.ErrInvalidDays):
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, analytics.ErrFeatureDisabled):
		httpx.WriteError(w, http.StatusForbidden, errFeatureMissing, nil)
	case err != nil:
		a.Log.ErrorContext(r.Context(), "analytics api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
	default:
		httpx.WriteJSON(w, http.StatusOK, analyticsFrom(report))
	}
}
