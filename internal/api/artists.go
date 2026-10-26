package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/artists"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
)

type artistJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sortName"`
	Type           string `json:"type"`
	Country        string `json:"country"`
	Disambiguation string `json:"disambiguation"`
}

func artistFrom(a dbgen.Artist) artistJSON {
	return artistJSON{
		ID: a.ID, Name: a.Name, SortName: a.SortName, Type: a.Type.String, Country: a.Country.String,
		Disambiguation: a.Disambiguation,
	}
}

type Artists struct {
	Svc *artists.Service
	Log *slog.Logger
}

func (c Artists) List(w http.ResponseWriter, r *http.Request) {
	list, err := c.Svc.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	skip, limit, err := httpx.Paginate(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	items := make([]artistJSON, len(list))
	for i, a := range list {
		items[i] = artistFrom(a)
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope(pageSlice(items, skip, limit), int64(len(items)), skip, limit, r))
}

func (c Artists) Show(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "artist not found", nil)
		return
	}
	a, err := c.Svc.Get(r.Context(), id)
	if errors.Is(err, artists.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "artist not found", nil)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, artistFrom(a))
}

func (c Artists) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body", nil)
		return
	}
	if utf8.RuneCountInString(body.Name) == 0 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", map[string]string{"name": "required"})
		return
	}
	a, err := c.Svc.Create(r.Context(), body.Name)
	if errors.Is(err, artists.ErrNameTooLong) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", map[string]string{"name": "length"})
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "artist created via api", "artist_id", a.ID, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteJSON(w, http.StatusCreated, artistFrom(a))
}

func (c Artists) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "artist api handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}
