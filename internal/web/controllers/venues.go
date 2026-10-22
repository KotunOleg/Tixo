package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/venues"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type venueListItem struct {
	ID      int64
	Name    string
	City    string
	Shared  bool
	EditURL string
}

type venueListData struct {
	Slug     string
	OrgName  string
	Items    []venueListItem
	ErrorMsg string
}

var venueListErrors = map[string]string{
	"has_events": "venue.delete.has_events",
}

type venueFormData struct {
	Slug     string
	OrgName  string
	ID       int64
	Editing  bool
	Name     string
	Address  string
	City     string
	Lat      string
	Lng      string
	Capacity string
	Errors   map[string]string
}

type Venues struct {
	View *view.Renderer
	Svc  *venues.Service
	Log  *slog.Logger
}

func (c Venues) List(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	rows, err := c.Svc.List(r.Context(), m.Organizer.ID)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	items := make([]venueListItem, len(rows))
	for i, v := range rows {
		shared := !v.OrganizerID.Valid
		items[i] = venueListItem{ID: v.ID, Name: v.Name, City: v.City, Shared: shared}
		if !shared {
			items[i].EditURL = "/org/" + m.Organizer.Slug + "/venues/" + strconv.FormatInt(v.ID, 10) + "/edit"
		}
	}
	c.View.Render(w, r, http.StatusOK, "pages/venue_list", venueListData{
		Slug: m.Organizer.Slug, OrgName: m.Organizer.Name, Items: items,
		ErrorMsg: venueListErrors[r.URL.Query().Get("error")],
	})
}

func (c Venues) New(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	c.View.Render(w, r, http.StatusOK, "pages/venue_form", venueFormData{Slug: m.Organizer.Slug, OrgName: m.Organizer.Name})
}

func (c Venues) Create(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		c.View.Render(w, r, http.StatusBadRequest, "pages/venue_form", venueFormData{Slug: m.Organizer.Slug, OrgName: m.Organizer.Name})
		return
	}
	form := venueFormFrom(r, m.Organizer.Slug, m.Organizer.Name, false, 0)
	in, ok := c.gateVenueInput(w, r, &form)
	if !ok {
		return
	}
	v, err := c.Svc.Create(r.Context(), m.Organizer.ID, in)
	var verr *venues.ValidationError
	if errors.As(err, &verr) {
		form.Errors = translateVenueErrors(verr)
		c.View.Render(w, r, http.StatusUnprocessableEntity, "pages/venue_form", form)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "venue created", "venue_id", v.ID, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/venues"), http.StatusSeeOther)
}

func (c Venues) gateVenueInput(w http.ResponseWriter, r *http.Request, form *venueFormData) (venues.Input, bool) {
	in, errs := parseVenueInput(*form)
	var verr *venues.ValidationError
	if err := venues.CheckInput(in); errors.As(err, &verr) {
		for field, code := range verr.Fields {
			if _, taken := errs[field]; !taken {
				errs[field] = "venue.form.error." + field + "_" + code
			}
		}
	}
	if len(errs) > 0 {
		form.Errors = errs
		c.View.Render(w, r, http.StatusUnprocessableEntity, "pages/venue_form", *form)
		return venues.Input{}, false
	}
	return in, true
}

func (c Venues) Edit(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	v, err := c.Svc.Get(r.Context(), m.Organizer.ID, id)
	if errors.Is(err, venues.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.View.Render(w, r, http.StatusOK, "pages/venue_form", venueFormDataFrom(v, m.Organizer.Slug, m.Organizer.Name, nil))
}

func (c Venues) Update(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		c.View.NotFound(w, r)
		return
	}
	form := venueFormFrom(r, m.Organizer.Slug, m.Organizer.Name, true, id)
	in, ok := c.gateVenueInput(w, r, &form)
	if !ok {
		return
	}
	v, err := c.Svc.Update(r.Context(), m.Organizer.ID, id, in)
	var verr *venues.ValidationError
	switch {
	case errors.Is(err, venues.ErrNotFound):
		c.View.NotFound(w, r)
		return
	case errors.As(err, &verr):
		form.Errors = translateVenueErrors(verr)
		c.View.Render(w, r, http.StatusUnprocessableEntity, "pages/venue_form", form)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "venue updated", "venue_id", v.ID, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/venues"), http.StatusSeeOther)
}

func (c Venues) Delete(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	err = c.Svc.Delete(r.Context(), m.Organizer.ID, id)
	switch {
	case errors.Is(err, venues.ErrNotFound):
		c.View.NotFound(w, r)
		return
	case errors.Is(err, venues.ErrHasEvents):
		c.redirectWithError(w, r, m.Organizer.Slug, "has_events")
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "venue deleted", "venue_id", id, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/venues"), http.StatusSeeOther)
}

func (c Venues) redirectWithError(w http.ResponseWriter, r *http.Request, slug, code string) {
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+slug+"/venues")+"?error="+code, http.StatusSeeOther)
}

func venueFormFrom(r *http.Request, slug, orgName string, editing bool, id int64) venueFormData {
	return venueFormData{
		Slug: slug, OrgName: orgName, ID: id, Editing: editing,
		Name: r.PostForm.Get("name"), Address: r.PostForm.Get("address"), City: r.PostForm.Get("city"),
		Lat: r.PostForm.Get("lat"), Lng: r.PostForm.Get("lng"), Capacity: r.PostForm.Get("capacity"),
	}
}

func venueFormDataFrom(v dbgen.Venue, slug, orgName string, errs map[string]string) venueFormData {
	return venueFormData{
		Slug: slug, OrgName: orgName, ID: v.ID, Editing: true,
		Name: v.Name, Address: v.Address, City: v.City,
		Lat: strconv.FormatFloat(v.Lat, 'f', -1, 64), Lng: strconv.FormatFloat(v.Lng, 'f', -1, 64),
		Capacity: strconv.FormatInt(int64(v.Capacity), 10), Errors: errs,
	}
}

func parseVenueInput(form venueFormData) (venues.Input, map[string]string) {
	errs := map[string]string{}
	lat, err := strconv.ParseFloat(strings.TrimSpace(form.Lat), 64)
	if err != nil {
		errs[venues.FieldLat] = "venue.form.error.lat_format"
	}
	lng, err := strconv.ParseFloat(strings.TrimSpace(form.Lng), 64)
	if err != nil {
		errs[venues.FieldLng] = "venue.form.error.lng_format"
	}
	capacity, err := strconv.ParseInt(strings.TrimSpace(form.Capacity), 10, 32)
	if err != nil {
		errs[venues.FieldCapacity] = "venue.form.error.capacity_format"
	}
	in := venues.Input{Name: form.Name, Address: form.Address, City: form.City, Lat: lat, Lng: lng, Capacity: int32(capacity)}
	return in, errs
}

func translateVenueErrors(verr *venues.ValidationError) map[string]string {
	out := make(map[string]string, len(verr.Fields))
	for field, code := range verr.Fields {
		out[field] = "venue.form.error." + field + "_" + code
	}
	return out
}

func (c Venues) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "venue handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}
