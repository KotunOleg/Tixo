package controllers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/analytics"
	"github.com/KotunOleg/Tixo/internal/features"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type analyticsPageData struct {
	Name   string
	Slug   string
	Report analytics.Report
}

type analyticsLockedData struct {
	Name      string
	Slug      string
	PlanLabel string
}

type Analytics struct {
	View *view.Renderer
	Svc  *analytics.Service
	Log  *slog.Logger
}

func (a Analytics) Show(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	report, err := a.Svc.Report(r.Context(), m, analytics.DefaultDays)
	if errors.Is(err, analytics.ErrFeatureDisabled) {
		renderFeatureLocked(a.View, w, r, m)
		return
	}
	if err != nil {
		a.Log.ErrorContext(r.Context(), "analytics page failed", "error", err, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
		a.View.ServerError(w, r)
		return
	}
	a.View.Render(w, r, http.StatusOK, "pages/org_analytics", analyticsPageData{Name: m.Organizer.Name, Slug: m.Organizer.Slug, Report: report})
}

func renderFeatureLocked(v *view.Renderer, w http.ResponseWriter, r *http.Request, m organizers.Membership) {
	v.Render(w, r, http.StatusForbidden, "pages/org_analytics_locked", analyticsLockedData{
		Name: m.Organizer.Name, Slug: m.Organizer.Slug, PlanLabel: "org.plan." + m.Organizer.Plan,
	})
}

func RequireFeature(v *view.Renderer, flags *features.Service, log *slog.Logger, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m := membershipFrom(r)
			on, err := flags.Enabled(r.Context(), m.Organizer.ID, name)
			if err != nil {
				log.ErrorContext(r.Context(), "feature check failed", "feature", name, "error", err, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
				v.ServerError(w, r)
				return
			}
			if !on {
				renderFeatureLocked(v, w, r, m)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
