package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
)

const maxJSONBodyBytes = 64 << 10
const cacheTTL = 60 * time.Second

func cacheStatus(hit bool) string {
	if hit {
		return "HIT"
	}
	return "MISS"
}

type membershipKey struct{}

func membershipFrom(r *http.Request) organizers.Membership {
	m, _ := r.Context().Value(membershipKey{}).(organizers.Membership)
	return m
}

func requireMember(svc *organizers.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, _ := auth.CurrentUser(r.Context())
			m, err := svc.ForMember(r.Context(), u.ID, chi.URLParam(r, "slug"))
			if errors.Is(err, organizers.ErrNotFound) {
				httpx.WriteError(w, http.StatusNotFound, "organizer not found", nil)
				return
			}
			if err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), membershipKey{}, m)))
		})
	}
}

type organizerJSON struct {
	ID       int64             `json:"id"`
	Slug     string            `json:"slug"`
	Name     string            `json:"name"`
	Plan     string            `json:"plan"`
	Role     string            `json:"role"`
	Contacts map[string]string `json:"contacts"`
}

func organizerFrom(m organizers.Membership) organizerJSON {
	return organizerJSON{
		ID: m.Organizer.ID, Slug: m.Organizer.Slug, Name: m.Organizer.Name, Plan: m.Organizer.Plan,
		Role: m.Role, Contacts: organizers.Contacts(m.Organizer),
	}
}

type Organizers struct {
	Svc *organizers.Service
	Log *slog.Logger
}

func (o Organizers) List(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.CurrentUser(r.Context())
	list, err := o.Svc.ListForUser(r.Context(), u.ID)
	if err != nil {
		o.serverError(w, r, err)
		return
	}
	skip, limit, err := httpx.Paginate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	items := make([]organizerJSON, len(list))
	for i, m := range list {
		items[i] = organizerFrom(m)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope(pageSlice(items, skip, limit), int64(len(items)), skip, limit, r))
}

func (o Organizers) Show(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, organizerFrom(membershipFrom(r)))
}

func (o Organizers) Update(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	if m.Role != organizers.RoleOwner {
		httpx.WriteError(w, http.StatusForbidden, "only the owner can update this organizer", nil)
		return
	}
	var body struct {
		Name     string `json:"name"`
		Contacts struct {
			Email    string `json:"email"`
			Phone    string `json:"phone"`
			Telegram string `json:"telegram"`
		} `json:"contacts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	updated, err := o.Svc.Update(r.Context(), m.Organizer, organizers.UpdateInput{
		Name: body.Name, ContactEmail: body.Contacts.Email, ContactPhone: body.Contacts.Phone, ContactTelegram: body.Contacts.Telegram,
	})
	var verr *organizers.ValidationError
	if errors.As(err, &verr) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", verr.Fields)
		return
	}
	if err != nil {
		o.serverError(w, r, err)
		return
	}
	o.Log.InfoContext(r.Context(), "organizer updated via api", "organizer_id", updated.ID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusOK, organizerFrom(organizers.Membership{Organizer: updated, Role: m.Role}))
}

func (o Organizers) serverError(w http.ResponseWriter, r *http.Request, err error) {
	o.Log.ErrorContext(r.Context(), "organizer api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}
