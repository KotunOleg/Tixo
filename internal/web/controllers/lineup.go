package controllers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/artists"
	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/lineup"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type Lineup struct {
	View     *view.Renderer
	Sessions *scs.SessionManager
	Svc      *lineup.Service
	Events   *events.Service
	Artists  *artists.Service
	Log      *slog.Logger
}

func (c Lineup) Add(w http.ResponseWriter, r *http.Request) {
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
	headliner := r.PostForm.Get("is_headliner") != ""

	artistID, err := strconv.ParseInt(r.PostForm.Get("artist_id"), 10, 64)
	if err != nil {
		name := strings.TrimSpace(r.PostForm.Get("new_artist_name"))
		if name == "" {
			c.back(w, r, m.Organizer.Slug, eventID, "lineup.form.error.required")
			return
		}
		a, err := c.Artists.Create(r.Context(), name)
		if err != nil {
			c.back(w, r, m.Organizer.Slug, eventID, "lineup.form.error.name")
			return
		}
		artistID = a.ID
	}

	err = c.Svc.Add(r.Context(), m.Organizer.ID, eventID, artistID, headliner)
	switch {
	case errors.Is(err, lineup.ErrEventNotFound):
		c.View.NotFound(w, r)
		return
	case errors.Is(err, lineup.ErrArtistNotFound):
		c.back(w, r, m.Organizer.Slug, eventID, "lineup.form.error.artist_not_found")
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "artist added to lineup", "artist_id", artistID, "event_id", eventID, "request_id", middleware.GetReqID(r.Context()))
	c.back(w, r, m.Organizer.Slug, eventID, "lineup.add.saved")
}

type artistSearchResult struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

func (c Lineup) Search(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	if _, err := c.Events.Get(r.Context(), m.Organizer.ID, eventID); errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	} else if err != nil {
		c.serverError(w, r, err)
		return
	}

	found, err := c.Artists.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	results := make([]artistSearchResult, len(found))
	for i, a := range found {
		results[i] = artistSearchResult{ID: a.ID, Label: a.Name}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		c.Log.ErrorContext(r.Context(), "lineup search encode failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	}
}

func (c Lineup) Remove(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	artistID, err := strconv.ParseInt(chi.URLParam(r, "artistID"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	err = c.Svc.Remove(r.Context(), m.Organizer.ID, eventID, artistID)
	if errors.Is(err, lineup.ErrEventNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "artist removed from lineup", "artist_id", artistID, "event_id", eventID, "request_id", middleware.GetReqID(r.Context()))
	c.back(w, r, m.Organizer.Slug, eventID, "lineup.remove.saved")
}

func (c Lineup) back(w http.ResponseWriter, r *http.Request, slug string, eventID int64, flash string) {
	auth.SetFlash(r.Context(), c.Sessions, flash)
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+slug+"/events/"+strconv.FormatInt(eventID, 10)), http.StatusSeeOther)
}

func (c Lineup) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "lineup handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}
