package controllers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
	_ "time/tzdata"

	"github.com/alexedwards/scs/v2"
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

type orgMemberRow struct {
	Name      string
	Email     string
	RoleLabel string
}

type orgShowData struct {
	Name        string
	Slug        string
	PlanLabel   string
	RoleLabel   string
	Events      []orgEventRow
	Members     []orgMemberRow
	CanManage   bool
	MemberEmail string
	MemberError string
}

type membershipKey struct{}

type Organizers struct {
	View     *view.Renderer
	Sessions *scs.SessionManager
	Svc      *organizers.Service
	Log      *slog.Logger
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
	o.renderShow(w, r, http.StatusOK, "", "")
}

func (o Organizers) AddMember(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	if m.Role != organizers.RoleOwner {
		o.View.Forbidden(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		o.renderShow(w, r, http.StatusBadRequest, "", "")
		return
	}
	email := r.PostForm.Get("email")
	added, err := o.Svc.AddMember(r.Context(), m, email)
	var verr *organizers.ValidationError
	switch {
	case errors.Is(err, organizers.ErrForbidden):
		o.View.Forbidden(w, r)
		return
	case errors.As(err, &verr):
		o.renderShow(w, r, http.StatusUnprocessableEntity, email, "org.form.error.email_"+verr.Fields[organizers.FieldEmail])
		return
	case err != nil:
		o.serverError(w, r, err)
		return
	}
	o.Log.InfoContext(r.Context(), "organizer member added", "organizer_id", m.Organizer.ID, "user_id", added.UserID, "request_id", middleware.GetReqID(r.Context()))
	auth.SetFlash(r.Context(), o.Sessions, "org.members.added")
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug), http.StatusSeeOther)
}

func (o Organizers) renderShow(w http.ResponseWriter, r *http.Request, status int, memberEmail, memberError string) {
	m := membershipFrom(r)
	events, err := o.Svc.Events(r.Context(), m)
	if err != nil {
		o.serverError(w, r, err)
		return
	}
	members, err := o.Svc.Members(r.Context(), m)
	if err != nil {
		o.serverError(w, r, err)
		return
	}
	rows := make([]orgEventRow, len(events))
	for i, e := range events {
		rows[i] = orgEventRow{Title: e.Title, StartsAt: e.StartsAt.In(kyiv).Format(dateLayout), StatusLabel: "org.event_status." + e.Status}
	}
	people := make([]orgMemberRow, len(members))
	for i, p := range members {
		people[i] = orgMemberRow{Name: p.Name, Email: p.Email, RoleLabel: "org.role." + p.Role}
	}
	o.View.Render(w, r, status, "pages/org_show", orgShowData{
		Name: m.Organizer.Name, Slug: m.Organizer.Slug,
		PlanLabel: "org.plan." + m.Organizer.Plan, RoleLabel: "org.role." + m.Role,
		Events: rows, Members: people, CanManage: m.Role == organizers.RoleOwner,
		MemberEmail: memberEmail, MemberError: memberError,
	})
}

func membershipFrom(r *http.Request) organizers.Membership {
	m, _ := r.Context().Value(membershipKey{}).(organizers.Membership)
	return m
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
