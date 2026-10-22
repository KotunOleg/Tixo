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
	"github.com/KotunOleg/Tixo/internal/lineup"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
)

type lineupEntryJSON struct {
	Artist      artistJSON `json:"artist"`
	Position    int32      `json:"position"`
	IsHeadliner bool       `json:"isHeadliner"`
}

func lineupEntryFrom(row dbgen.ListEventArtistsForOrganizerRow) lineupEntryJSON {
	return lineupEntryJSON{Artist: artistFrom(row.Artist), Position: row.Position, IsHeadliner: row.IsHeadliner}
}

type Lineup struct {
	Svc *lineup.Service
	Log *slog.Logger
}

func (c Lineup) List(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	rows, err := c.Svc.List(r.Context(), m.Organizer.ID, eventID)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	skip, limit, err := httpx.Paginate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	items := make([]lineupEntryJSON, len(rows))
	for i, row := range rows {
		items[i] = lineupEntryFrom(row)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope(pageSlice(items, skip, limit), int64(len(items)), skip, limit, r))
}

func (c Lineup) Add(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	var body struct {
		ArtistID    int64 `json:"artistId"`
		IsHeadliner bool  `json:"isHeadliner"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	if body.ArtistID == 0 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", map[string]string{"artistId": "required"})
		return
	}
	err = c.Svc.Add(r.Context(), m.Organizer.ID, eventID, body.ArtistID, body.IsHeadliner)
	switch {
	case errors.Is(err, lineup.ErrEventNotFound):
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	case errors.Is(err, lineup.ErrArtistNotFound):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", map[string]string{"artistId": "not_found"})
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "lineup artist added via api", "event_id", eventID, "artist_id", body.ArtistID, "request_id", middleware.GetReqID(r.Context()))
	w.WriteHeader(http.StatusCreated)
}

func (c Lineup) Remove(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	eventID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	}
	artistID, err := strconv.ParseInt(chi.URLParam(r, "artistID"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "artist not found", nil)
		return
	}
	if err := c.Svc.Remove(r.Context(), m.Organizer.ID, eventID, artistID); errors.Is(err, lineup.ErrEventNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "event not found", nil)
		return
	} else if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "lineup artist removed via api", "event_id", eventID, "artist_id", artistID, "request_id", middleware.GetReqID(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

func (c Lineup) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "lineup api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}
