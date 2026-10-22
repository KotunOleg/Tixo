package controllers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
	_ "time/tzdata"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

const (
	maxFormBytes = 64 << 10
	dateLayout   = "02.01.2006 15:04"
)

var kyiv = mustLocation("Europe/Kyiv")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type orgListItem struct {
	Name      string
	Slug      string
	Path      string
	RoleLabel string
}

type orgListData struct {
	Items []orgListItem
}

type orgFormData struct {
	Name   string
	Slug   string
	Errors map[string]string
}

type orgEventRow struct {
	Title       string
	StartsAt    string
	StatusLabel string
}

type orgShowData struct {
	Name      string
	Slug      string
	PlanLabel string
	RoleLabel string
	Events    []orgEventRow
}

type membershipKey struct{}

type Organizers struct {
	View *view.Renderer
	Svc  *organizers.Service
	Log  *slog.Logger
}

func (o Organizers) List(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.CurrentUser(r.Context())
	list, err := o.Svc.ListForUser(r.Context(), u.ID)
	if err != nil {
		o.serverError(w, r, err)
		return
	}
	items := make([]orgListItem, len(list))
	for i, m := range list {
		items[i] = orgListItem{Name: m.Organizer.Name, Slug: m.Organizer.Slug, Path: "/org/" + m.Organizer.Slug, RoleLabel: "org.role." + m.Role}
	}
	o.View.Render(w, r, http.StatusOK, "pages/org_list", orgListData{Items: items})
}

func (o Organizers) New(w http.ResponseWriter, r *http.Request) {
	o.View.Render(w, r, http.StatusOK, "pages/org_new", orgFormData{})
}

func (o Organizers) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		o.View.Render(w, r, http.StatusBadRequest, "pages/org_new", orgFormData{})
		return
	}
	form := orgFormData{Name: r.PostForm.Get("name"), Slug: r.PostForm.Get("slug")}
	u, _ := auth.CurrentUser(r.Context())
	org, err := o.Svc.Create(r.Context(), u.ID, organizers.CreateInput{Name: form.Name, Slug: form.Slug})
	var verr *organizers.ValidationError
	if errors.As(err, &verr) {
		form.Errors = make(map[string]string, len(verr.Fields))
		for field, code := range verr.Fields {
			form.Errors[field] = "org.form.error." + field + "_" + code
		}
		o.View.Render(w, r, http.StatusUnprocessableEntity, "pages/org_new", form)
		return
	}
	if err != nil {
		o.serverError(w, r, err)
		return
	}
	o.Log.InfoContext(r.Context(), "organizer created", "organizer_id", org.ID, "user_id", u.ID, "request_id", middleware.GetReqID(r.Context()))
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+org.Slug), http.StatusSeeOther)
}

func (o Organizers) Show(w http.ResponseWriter, r *http.Request) {
	m := r.Context().Value(membershipKey{}).(organizers.Membership)
	events, err := o.Svc.Events(r.Context(), m)
	if err != nil {
		o.serverError(w, r, err)
		return
	}
	rows := make([]orgEventRow, len(events))
	for i, e := range events {
		rows[i] = orgEventRow{Title: e.Title, StartsAt: e.StartsAt.In(kyiv).Format(dateLayout), StatusLabel: "org.event_status." + e.Status}
	}
	o.View.Render(w, r, http.StatusOK, "pages/org_show", orgShowData{
		Name: m.Organizer.Name, Slug: m.Organizer.Slug,
		PlanLabel: "org.plan." + m.Organizer.Plan, RoleLabel: "org.role." + m.Role,
		Events: rows,
	})
}

func (o Organizers) RequireMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.CurrentUser(r.Context())
		m, err := o.Svc.ForMember(r.Context(), u.ID, chi.URLParam(r, "slug"))
		if errors.Is(err, organizers.ErrNotFound) {
			o.View.NotFound(w, r)
			return
		}
		if err != nil {
			o.serverError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), membershipKey{}, m)))
	})
}

func (o Organizers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	o.Log.ErrorContext(r.Context(), "organizer handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	o.View.ServerError(w, r)
}
