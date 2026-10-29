package controllers

import (
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/KotunOleg/Tixo/internal/artists"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/lineup"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/venues"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

const (
	datetimeLocalLayout = "2006-01-02T15:04"
	maxPosterBytes      = 5 << 20
	maxEventFormBytes   = maxPosterBytes + maxFormBytes
	eventsPageSize      = 50
)

var (
	errBadPrice = errors.New("invalid price")

	posterExt = map[string]string{
		"image/jpeg": "jpg",
		"image/png":  "png",
		"image/webp": "webp",
	}
)

type eventListItem struct {
	ID          int64
	Title       string
	StartsAt    string
	Status      string
	StatusLabel string
}

type eventListData struct {
	Slug     string
	OrgName  string
	Items    []eventListItem
	ErrorMsg string
}

var eventListErrors = map[string]string{
	"has_tickets": "event.delete.has_tickets",
}

type eventVenueOption struct {
	ID       string
	Label    string
	Selected bool
}

type eventGenreOption struct {
	Value    string
	Label    string
	Selected bool
}

type eventFormData struct {
	Slug        string
	OrgName     string
	ID          int64
	Editing     bool
	Title       string
	EventSlug   string
	Description string
	VenueID     string
	Venues      []eventVenueOption
	StartsAt    string
	EndsAt      string
	Genre       string
	Genres      []eventGenreOption
	PriceFrom   string
	HasPoster   bool
	PosterURL   string
	Errors      map[string]string
}

type eventShowData struct {
	Slug        string
	OrgName     string
	ID          int64
	Title       string
	StatusLabel string
	Status      string
	PosterURL   string
	Tiers       []tierRow
	Lineup      []lineupRow
	LineupQuery string
	ArtistHits  []artistOption
}

type tierRow struct {
	ID        int64
	Name      string
	Price     string
	Quantity  string
	Available int32
}

type lineupRow struct {
	ArtistID    int64
	Name        string
	IsHeadliner bool
}

type artistOption struct {
	ID    int64
	Label string
}

type Events struct {
	View    *view.Renderer
	Svc     *events.Service
	Venues  *venues.Service
	Tiers   *tiers.Service
	Lineup  *lineup.Service
	Artists *artists.Service
	Storage storage.Store
	Log     *slog.Logger
}

func (c Events) List(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	rows, err := c.Svc.List(r.Context(), m.Organizer.ID, eventsPageSize, 0)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	items := make([]eventListItem, len(rows))
	for i, e := range rows {
		items[i] = eventListItem{
			ID: e.ID, Title: e.Title, StartsAt: e.StartsAt.In(kyiv).Format(dateLayout),
			Status: e.Status, StatusLabel: "org.event_status." + e.Status,
		}
	}
	c.View.Render(w, r, http.StatusOK, "pages/event_list", eventListData{
		Slug: m.Organizer.Slug, OrgName: m.Organizer.Name, Items: items,
		ErrorMsg: eventListErrors[r.URL.Query().Get("error")],
	})
}

func (c Events) New(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	opts, err := c.venueOptions(r, "")
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.View.Render(w, r, http.StatusOK, "pages/event_form", eventFormData{
		Slug: m.Organizer.Slug, OrgName: m.Organizer.Name, Venues: opts, Genres: genreOptions(""),
	})
}

func (c Events) Create(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxEventFormBytes)
	if err := r.ParseMultipartForm(maxEventFormBytes); err != nil {
		c.renderTooLarge(w, r, eventFormData{Slug: m.Organizer.Slug, OrgName: m.Organizer.Name})
		return
	}
	form, err := c.formFromRequest(r, m.Organizer.Slug, m.Organizer.Name, false, 0)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	in, _, uploadedKey, ok := c.gateEventInput(w, r, &form, "")
	if !ok {
		return
	}
	e, err := c.Svc.Create(r.Context(), m.Organizer.ID, in)
	var verr *events.ValidationError
	if errors.As(err, &verr) {
		if uploadedKey != "" {
			_ = c.Storage.Delete(uploadedKey)
		}
		form.Errors = translateEventErrors(verr)
		c.View.Render(w, r, http.StatusUnprocessableEntity, "pages/event_form", form)
		return
	}
	if err != nil {
		if uploadedKey != "" {
			_ = c.Storage.Delete(uploadedKey)
		}
		c.serverError(w, r, err)
		return
	}
	c.Log.InfoContext(r.Context(), "event created", "event_id", e.ID, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/events"), http.StatusSeeOther)
}

func (c Events) Show(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	e, err := c.Svc.Get(r.Context(), m.Organizer.ID, id)
	if errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	rows, err := c.Tiers.List(r.Context(), m.Organizer.ID, e.ID)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	tierRows := make([]tierRow, len(rows))
	for i, t := range rows {
		tierRows[i] = tierRow{ID: t.ID, Name: t.Name, Price: formatPrice(t.Price), Quantity: strconv.FormatInt(int64(t.Quantity), 10), Available: t.Available}
	}
	lineupData, err := c.lineupData(r, e.ID)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.View.Render(w, r, http.StatusOK, "pages/event_show", eventShowData{
		Slug: m.Organizer.Slug, OrgName: m.Organizer.Name, ID: e.ID, Title: e.Title, Status: e.Status,
		StatusLabel: "org.event_status." + e.Status, PosterURL: posterURL(e.PosterKey), Tiers: tierRows,
		Lineup: lineupData.lineup, LineupQuery: lineupData.query, ArtistHits: lineupData.hits,
	})
}

type lineupShowData struct {
	lineup []lineupRow
	query  string
	hits   []artistOption
}

func (c Events) lineupData(r *http.Request, eventID int64) (lineupShowData, error) {
	m := membershipFrom(r)
	rows, err := c.Lineup.List(r.Context(), m.Organizer.ID, eventID)
	if err != nil {
		return lineupShowData{}, err
	}
	lineupRows := make([]lineupRow, len(rows))
	inLineup := make(map[int64]bool, len(rows))
	for i, row := range rows {
		lineupRows[i] = lineupRow{ArtistID: row.Artist.ID, Name: row.Artist.Name, IsHeadliner: row.IsHeadliner}
		inLineup[row.Artist.ID] = true
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var hits []artistOption
	if query != "" {
		found, err := c.Artists.Search(r.Context(), query)
		if err != nil {
			return lineupShowData{}, err
		}
		for _, a := range found {
			if inLineup[a.ID] {
				continue
			}
			hits = append(hits, artistOption{ID: a.ID, Label: a.Name})
		}
	}
	return lineupShowData{lineup: lineupRows, query: query, hits: hits}, nil
}

func (c Events) Edit(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	e, err := c.Svc.Get(r.Context(), m.Organizer.ID, id)
	if errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	form, err := c.formFromEvent(r, m.Organizer.Slug, m.Organizer.Name, e)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.View.Render(w, r, http.StatusOK, "pages/event_form", form)
}

func (c Events) Update(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	existing, err := c.Svc.Get(r.Context(), m.Organizer.ID, id)
	if errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEventFormBytes)
	if err := r.ParseMultipartForm(maxEventFormBytes); err != nil {
		form, ferr := c.formFromEvent(r, m.Organizer.Slug, m.Organizer.Name, existing)
		if ferr != nil {
			c.serverError(w, r, ferr)
			return
		}
		c.renderTooLarge(w, r, form)
		return
	}
	form, err := c.formFromRequest(r, m.Organizer.Slug, m.Organizer.Name, true, id)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	in, oldPosterKey, uploadedKey, ok := c.gateEventInput(w, r, &form, existing.PosterKey.String)
	if !ok {
		return
	}
	e, err := c.Svc.Update(r.Context(), m.Organizer.ID, id, in)
	var verr *events.ValidationError
	switch {
	case errors.Is(err, events.ErrNotFound):
		if uploadedKey != "" {
			_ = c.Storage.Delete(uploadedKey)
		}
		c.View.NotFound(w, r)
		return
	case errors.As(err, &verr):
		if uploadedKey != "" {
			_ = c.Storage.Delete(uploadedKey)
		}
		form.Errors = translateEventErrors(verr)
		c.View.Render(w, r, http.StatusUnprocessableEntity, "pages/event_form", form)
		return
	case err != nil:
		if uploadedKey != "" {
			_ = c.Storage.Delete(uploadedKey)
		}
		c.serverError(w, r, err)
		return
	}
	if oldPosterKey != "" {
		_ = c.Storage.Delete(oldPosterKey)
	}
	c.Log.InfoContext(r.Context(), "event updated", "event_id", e.ID, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/events/"+strconv.FormatInt(e.ID, 10)), http.StatusSeeOther)
}

func (c Events) SetStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := membershipFrom(r)
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			c.View.NotFound(w, r)
			return
		}
		e, err := c.Svc.SetStatus(r.Context(), m.Organizer.ID, id, status)
		if errors.Is(err, events.ErrNotFound) {
			c.View.NotFound(w, r)
			return
		}
		if err != nil {
			c.serverError(w, r, err)
			return
		}
		c.Log.InfoContext(r.Context(), "event status changed", "event_id", e.ID, "status", status, "request_id", middleware.GetReqID(r.Context()))
		redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/events/"+strconv.FormatInt(e.ID, 10)), http.StatusSeeOther)
	}
}

func (c Events) Delete(w http.ResponseWriter, r *http.Request) {
	m := membershipFrom(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		c.View.NotFound(w, r)
		return
	}
	e, err := c.Svc.Get(r.Context(), m.Organizer.ID, id)
	if errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	err = c.Svc.Delete(r.Context(), m.Organizer.ID, id)
	switch {
	case errors.Is(err, events.ErrHasTickets):
		redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/events")+"?error=has_tickets", http.StatusSeeOther)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	if e.PosterKey.Valid {
		_ = c.Storage.Delete(e.PosterKey.String)
	}
	c.Log.InfoContext(r.Context(), "event deleted", "event_id", id, "organizer_id", m.Organizer.ID, "request_id", middleware.GetReqID(r.Context()))
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/org/"+m.Organizer.Slug+"/events"), http.StatusSeeOther)
}

func (c Events) renderTooLarge(w http.ResponseWriter, r *http.Request, form eventFormData) {
	form.Errors = map[string]string{"poster": "event.form.error.poster_size"}
	c.View.Render(w, r, http.StatusRequestEntityTooLarge, "pages/event_form", form)
}

func (c Events) formFromEvent(r *http.Request, slug, orgName string, e dbgen.Event) (eventFormData, error) {
	venueID := strconv.FormatInt(e.VenueID, 10)
	opts, err := c.venueOptions(r, venueID)
	if err != nil {
		return eventFormData{}, err
	}
	price := ""
	if e.PriceFrom.Valid {
		price = formatPrice(e.PriceFrom.Int64)
	}
	return eventFormData{
		Slug: slug, OrgName: orgName, ID: e.ID, Editing: true,
		Title: e.Title, EventSlug: e.Slug, Description: e.Description, VenueID: venueID, Venues: opts,
		StartsAt: e.StartsAt.In(kyiv).Format(datetimeLocalLayout), EndsAt: e.EndsAt.In(kyiv).Format(datetimeLocalLayout),
		Genre: e.Genre, Genres: genreOptions(e.Genre), PriceFrom: price,
		HasPoster: e.PosterKey.Valid, PosterURL: posterURL(e.PosterKey),
	}, nil
}

func (c Events) formFromRequest(r *http.Request, slug, orgName string, editing bool, id int64) (eventFormData, error) {
	get := func(name string) string {
		if r.MultipartForm == nil || len(r.MultipartForm.Value[name]) == 0 {
			return ""
		}
		return r.MultipartForm.Value[name][0]
	}
	venueID := get("venue_id")
	opts, err := c.venueOptions(r, venueID)
	if err != nil {
		return eventFormData{}, err
	}
	return eventFormData{
		Slug: slug, OrgName: orgName, ID: id, Editing: editing,
		Title: get("title"), EventSlug: get("slug"), Description: get("description"), VenueID: venueID, Venues: opts,
		StartsAt: get("starts_at"), EndsAt: get("ends_at"), Genre: get("genre"), Genres: genreOptions(get("genre")),
		PriceFrom: get("price_from"),
	}, nil
}

func (c Events) venueOptions(r *http.Request, selected string) ([]eventVenueOption, error) {
	m := membershipFrom(r)
	rows, err := c.Venues.List(r.Context(), m.Organizer.ID)
	if err != nil {
		return nil, err
	}
	opts := make([]eventVenueOption, len(rows))
	for i, v := range rows {
		id := strconv.FormatInt(v.ID, 10)
		opts[i] = eventVenueOption{ID: id, Label: v.Name + " — " + v.City, Selected: id == selected}
	}
	return opts, nil
}

func genreOptions(selected string) []eventGenreOption {
	list := events.Genres()
	opts := make([]eventGenreOption, len(list))
	for i, g := range list {
		opts[i] = eventGenreOption{Value: g, Label: "genre." + g, Selected: g == selected}
	}
	return opts
}

func (c Events) gateEventInput(w http.ResponseWriter, r *http.Request, form *eventFormData, currentPosterKey string) (events.Input, string, string, bool) {
	errs := map[string]string{}
	venueID, err := strconv.ParseInt(form.VenueID, 10, 64)
	if err != nil {
		errs[events.FieldVenue] = "event.form.error.venue_id_required"
	}
	startsAt, startErr := time.ParseInLocation(datetimeLocalLayout, form.StartsAt, kyiv)
	if startErr != nil {
		errs[events.FieldStartsAt] = "event.form.error.starts_at_format"
	}
	endsAt, endErr := time.ParseInLocation(datetimeLocalLayout, form.EndsAt, kyiv)
	if endErr != nil {
		errs[events.FieldEndsAt] = "event.form.error.ends_at_format"
	}
	price, priceErr := parsePrice(form.PriceFrom)
	if priceErr != nil {
		errs[events.FieldPriceFrom] = "event.form.error.price_from_format"
	}

	in := events.Input{
		VenueID: venueID, Slug: form.EventSlug, Title: form.Title, Description: form.Description,
		StartsAt: startsAt, EndsAt: endsAt, Genre: form.Genre, PriceFrom: price, PosterKey: currentPosterKey,
	}
	var verr *events.ValidationError
	if err := events.CheckInput(in); errors.As(err, &verr) {
		for field, code := range verr.Fields {
			if _, taken := errs[field]; !taken {
				errs[field] = "event.form.error." + field + "_" + code
			}
		}
	}
	if len(errs) > 0 {
		form.Errors = errs
		c.View.Render(w, r, http.StatusUnprocessableEntity, "pages/event_form", *form)
		return events.Input{}, "", "", false
	}

	newPosterKey, oldPosterKey, uploadedKey, ok := c.resolvePoster(w, r, form, currentPosterKey)
	if !ok {
		return events.Input{}, "", "", false
	}
	in.PosterKey = newPosterKey
	return in, oldPosterKey, uploadedKey, true
}

func (c Events) resolvePoster(w http.ResponseWriter, r *http.Request, form *eventFormData, currentPosterKey string) (string, string, string, bool) {
	posterKey := currentPosterKey
	if r.MultipartForm != nil && len(r.MultipartForm.Value["remove_poster"]) > 0 {
		posterKey = ""
	}
	uploadedKey := ""
	file, header, err := r.FormFile("poster")
	if err == nil {
		defer func() { _ = file.Close() }()
		key, code := c.uploadPoster(file, header)
		if code != "" {
			form.Errors = map[string]string{"poster": "event.form.error.poster_" + code}
			c.View.Render(w, r, http.StatusUnprocessableEntity, "pages/event_form", *form)
			return "", "", "", false
		}
		posterKey = key
		uploadedKey = key
	}
	oldPosterKey := ""
	if currentPosterKey != "" && currentPosterKey != posterKey {
		oldPosterKey = currentPosterKey
	}
	return posterKey, oldPosterKey, uploadedKey, true
}

func (c Events) uploadPoster(f multipart.File, header *multipart.FileHeader) (string, string) {
	if header.Size > maxPosterBytes {
		return "", "size"
	}
	head := make([]byte, 512)
	n, _ := f.Read(head)
	ct := http.DetectContentType(head[:n])
	ext, ok := posterExt[ct]
	if !ok {
		return "", "type"
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", "io"
	}
	key, err := c.Storage.Save(f, ext)
	if err != nil {
		return "", "io"
	}
	return key, ""
}

func translateEventErrors(verr *events.ValidationError) map[string]string {
	out := make(map[string]string, len(verr.Fields))
	for field, code := range verr.Fields {
		out[field] = "event.form.error." + field + "_" + code
	}
	return out
}

func posterURL(key pgtype.Text) string {
	if !key.Valid || key.String == "" {
		return ""
	}
	return "/media/" + key.String
}

func parsePrice(s string) (*int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	wholeNum, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || wholeNum < 0 {
		return nil, errBadPrice
	}
	cents := int64(0)
	if hasFrac {
		if len(frac) == 0 || len(frac) > 2 {
			return nil, errBadPrice
		}
		for len(frac) < 2 {
			frac += "0"
		}
		cents, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return nil, errBadPrice
		}
	}
	v := wholeNum*100 + cents
	return &v, nil
}

func formatPrice(kopecks int64) string {
	return strconv.FormatInt(kopecks/100, 10) + "." + pad2(kopecks%100)
}

func pad2(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

func (c Events) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "event handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}
