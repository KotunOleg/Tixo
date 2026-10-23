package controllers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/KotunOleg/Tixo/internal/auth"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

const (
	saleOpen      = "open"
	saleCancelled = "cancelled"
	saleEnded     = "ended"
	saleNoTiers   = "no_tiers"
	saleSoldOut   = "sold_out"

	homeUpcomingLimit = 6
)

var telegramHandle = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)

type landingTier struct {
	ID        int64
	Name      string
	Price     int64
	Available int
}

type landingArtist struct {
	Name      string
	Headliner bool
}

type landingContact struct {
	Label string
	Text  string
	Href  string
}

type landingData struct {
	Slug          string
	Title         string
	Description   string
	Genre         string
	StartsAt      time.Time
	EndsAt        time.Time
	PosterURL     string
	VenueName     string
	VenueAddress  string
	VenueCity     string
	VenueLat      float64
	VenueLng      float64
	MapURL        string
	OrganizerName string
	Contacts      []landingContact
	Lineup        []landingArtist
	Tiers         []landingTier
	PriceFrom     int64
	Sale          string
	Buy           buyForm
}

type buyForm struct {
	Key      string
	TierID   int64
	Quantity int
	Errors   map[string]string
}

func (d landingData) OnSale() bool { return d.Sale == saleOpen }

func (d landingData) Quantities() []int {
	out := make([]int, orders.MaxQuantity)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

type PublicEvents struct {
	View   *view.Renderer
	Svc    *events.Service
	Orders *orders.Service
	Log    *slog.Logger
}

func (c PublicEvents) Show(w http.ResponseWriter, r *http.Request) {
	pub, err := c.Svc.PublicBySlug(r.Context(), chi.URLParam(r, "slug"))
	if errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	d := landingFrom(pub, time.Now())
	d.Buy = buyForm{Key: uuid.NewString(), TierID: firstAvailable(d.Tiers), Quantity: 1}
	c.View.Render(w, r, http.StatusOK, "pages/public_event", d)
}

func (c PublicEvents) Buy(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	lang := i18n.FromContext(r.Context())
	u, ok := auth.CurrentUser(r.Context())
	if !ok {
		redirectLocal(w, i18n.Path(lang, "/auth/google/login")+"?next="+url.QueryEscape(i18n.Path(lang, "/events/"+slug)), http.StatusSeeOther)
		return
	}
	pub, err := c.Svc.PublicBySlug(r.Context(), slug)
	if errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		c.View.NotFound(w, r)
		return
	}
	form := buyForm{Key: r.PostFormValue("idempotency_key"), Errors: map[string]string{}}
	form.TierID, _ = strconv.ParseInt(r.PostFormValue("tier_id"), 10, 64)
	qty, qtyErr := strconv.ParseInt(r.PostFormValue("quantity"), 10, 32)
	form.Quantity = int(qty)
	key, keyErr := uuid.Parse(form.Key)
	switch {
	case keyErr != nil || key == uuid.Nil:
		form.Key = uuid.NewString()
		form.Errors[orders.FieldKey] = "landing.buy.error.expired"
	case qtyErr != nil || qty < orders.MinQuantity || qty > orders.MaxQuantity:
		form.Errors[orders.FieldQuantity] = "landing.buy.error.quantity"
	}
	if len(form.Errors) > 0 {
		c.renderBuy(w, r, pub, form, http.StatusUnprocessableEntity)
		return
	}

	order, replay, err := c.Orders.Purchase(r.Context(), u.ID, orders.Input{EventID: pub.Event.ID, TierID: form.TierID, Quantity: int32(qty), Key: key})
	var verr *orders.ValidationError
	switch {
	case errors.As(err, &verr), errors.Is(err, orders.ErrNotFound):
		form.Errors[orders.FieldTier] = "landing.buy.error.tier"
		c.renderBuy(w, r, pub, form, http.StatusUnprocessableEntity)
		return
	case errors.Is(err, orders.ErrSoldOut):
		form.Errors[orders.FieldTier] = "landing.buy.error.sold_out"
		c.renderFresh(w, r, slug, form)
		return
	case errors.Is(err, orders.ErrNotOnSale):
		form.Errors[orders.FieldTier] = "landing.buy.error.not_on_sale"
		c.renderFresh(w, r, slug, form)
		return
	case errors.Is(err, orders.ErrKeyReused):
		form.Key = uuid.NewString()
		form.Errors[orders.FieldKey] = "landing.buy.error.reused"
		c.renderBuy(w, r, pub, form, http.StatusUnprocessableEntity)
		return
	case err != nil:
		c.serverError(w, r, err)
		return
	}
	if !replay {
		c.Log.InfoContext(r.Context(), "order created", "order_id", order.ID, "user_id", u.ID, "tier_id", order.TierID, "quantity", order.Quantity, "request_id", middleware.GetReqID(r.Context()))
	}
	redirectLocal(w, i18n.Path(lang, "/orders/"+strconv.FormatInt(order.ID, 10)), http.StatusSeeOther)
}

func (c PublicEvents) renderFresh(w http.ResponseWriter, r *http.Request, slug string, form buyForm) {
	pub, err := c.Svc.PublicBySlug(r.Context(), slug)
	if err != nil {
		c.serverError(w, r, err)
		return
	}
	c.renderBuy(w, r, pub, form, http.StatusConflict)
}

func (c PublicEvents) renderBuy(w http.ResponseWriter, r *http.Request, pub events.Public, form buyForm, status int) {
	d := landingFrom(pub, time.Now())
	d.Buy = form
	c.View.Render(w, r, status, "pages/public_event", d)
}

func (c PublicEvents) serverError(w http.ResponseWriter, r *http.Request, err error) {
	c.Log.ErrorContext(r.Context(), "public event handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	c.View.ServerError(w, r)
}

func firstAvailable(tiers []landingTier) int64 {
	for _, t := range tiers {
		if t.Available > 0 {
			return t.ID
		}
	}
	return 0
}

func landingFrom(pub events.Public, now time.Time) landingData {
	e, v := pub.Event, pub.Venue
	d := landingData{
		Slug: e.Slug, Title: e.Title, Description: strings.TrimSpace(e.Description), Genre: "genre." + e.Genre,
		StartsAt: e.StartsAt, EndsAt: e.EndsAt, PosterURL: posterURL(e.PosterKey),
		VenueName: v.Name, VenueAddress: v.Address, VenueCity: v.City, VenueLat: v.Lat, VenueLng: v.Lng, MapURL: osmURL(v),
		OrganizerName: pub.OrganizerName, Contacts: contactsFrom(pub.Contacts),
	}
	for _, a := range pub.Lineup {
		d.Lineup = append(d.Lineup, landingArtist{Name: a.Artist.Name, Headliner: a.IsHeadliner})
	}
	d.Tiers, d.PriceFrom = tiersFrom(pub.Tiers)
	d.Sale = saleState(e, d.Tiers, now)
	return d
}

func tiersFrom(rows []dbgen.TicketTier) ([]landingTier, int64) {
	tiers := make([]landingTier, len(rows))
	var from, fromAny int64 = -1, -1
	for i, t := range rows {
		tiers[i] = landingTier{ID: t.ID, Name: t.Name, Price: t.Price, Available: int(t.Available)}
		if fromAny < 0 || t.Price < fromAny {
			fromAny = t.Price
		}
		if t.Available > 0 && (from < 0 || t.Price < from) {
			from = t.Price
		}
	}
	if from < 0 {
		from = fromAny
	}
	return tiers, max(from, 0)
}

func saleState(e dbgen.Event, tiers []landingTier, now time.Time) string {
	switch {
	case e.Status == events.StatusCancelled:
		return saleCancelled
	case !e.StartsAt.After(now):
		return saleEnded
	case len(tiers) == 0:
		return saleNoTiers
	}
	for _, t := range tiers {
		if t.Available > 0 {
			return saleOpen
		}
	}
	return saleSoldOut
}

func contactsFrom(c map[string]string) []landingContact {
	var out []landingContact
	if email := c[organizers.ContactKeyEmail]; email != "" {
		out = append(out, landingContact{Label: "landing.contact.email", Text: email, Href: "mailto:" + email})
	}
	if phone := c[organizers.ContactKeyPhone]; phone != "" {
		contact := landingContact{Label: "landing.contact.phone", Text: phone}
		if digits := phoneDigits(phone); len(digits) >= 5 {
			contact.Href = "tel:" + digits
		}
		out = append(out, contact)
	}
	if tg := c[organizers.ContactKeyTelegram]; tg != "" {
		contact := landingContact{Label: "landing.contact.telegram", Text: tg}
		handle := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(tg, "https://t.me/"), "t.me/"), "@")
		if telegramHandle.MatchString(handle) {
			contact.Text, contact.Href = "@"+handle, "https://t.me/"+handle
		}
		out = append(out, contact)
	}
	return out
}

func phoneDigits(s string) string {
	var b strings.Builder
	for i, r := range s {
		if (r >= '0' && r <= '9') || (r == '+' && i == 0) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func osmURL(v dbgen.Venue) string {
	return fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.6f&mlon=%.6f#map=17/%.6f/%.6f", v.Lat, v.Lng, v.Lat, v.Lng)
}
