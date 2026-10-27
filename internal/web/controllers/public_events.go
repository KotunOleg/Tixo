package controllers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/organizers"
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
}

func (d landingData) OnSale() bool { return d.Sale == saleOpen }

type PublicEvents struct {
	View *view.Renderer
	Svc  *events.Service
	Log  *slog.Logger
}

func (c PublicEvents) Show(w http.ResponseWriter, r *http.Request) {
	pub, err := c.Svc.PublicBySlug(r.Context(), chi.URLParam(r, "slug"))
	if errors.Is(err, events.ErrNotFound) {
		c.View.NotFound(w, r)
		return
	}
	if err != nil {
		c.Log.ErrorContext(r.Context(), "public event page failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		c.View.ServerError(w, r)
		return
	}
	c.View.Render(w, r, http.StatusOK, "pages/public_event", landingFrom(pub, time.Now()))
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
