package controllers

import (
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

const (
	metaDescriptionLen = 155
	schemaOrg          = "https://schema.org/"
)

type ldEvent struct {
	Context        string    `json:"@context"`
	Type           string    `json:"@type"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	StartDate      string    `json:"startDate"`
	EndDate        string    `json:"endDate"`
	EventStatus    string    `json:"eventStatus"`
	AttendanceMode string    `json:"eventAttendanceMode"`
	URL            string    `json:"url"`
	Image          []string  `json:"image,omitempty"`
	Location       ldPlace   `json:"location"`
	Organizer      ldNamed   `json:"organizer"`
	Performer      []ldNamed `json:"performer,omitempty"`
	Offers         []ldOffer `json:"offers,omitempty"`
}

type ldPlace struct {
	Type    string    `json:"@type"`
	Name    string    `json:"name"`
	Address ldAddress `json:"address"`
	Geo     ldGeo     `json:"geo"`
}

type ldAddress struct {
	Type     string `json:"@type"`
	Street   string `json:"streetAddress,omitempty"`
	Locality string `json:"addressLocality"`
}

type ldGeo struct {
	Type      string  `json:"@type"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type ldNamed struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

type ldOffer struct {
	Type         string `json:"@type"`
	Name         string `json:"name"`
	Price        string `json:"price"`
	Currency     string `json:"priceCurrency"`
	Availability string `json:"availability"`
	URL          string `json:"url"`
}

func eventLD(d landingData, pub events.Public, pageURL, imageURL string) ldEvent {
	status := schemaOrg + "EventScheduled"
	if d.Sale == saleCancelled {
		status = schemaOrg + "EventCancelled"
	}
	ld := ldEvent{
		Context: "https://schema.org", Type: "Event", Name: d.Title, Description: d.Description,
		StartDate: d.StartsAt.In(kyiv).Format(time.RFC3339), EndDate: d.EndsAt.In(kyiv).Format(time.RFC3339),
		EventStatus: status, AttendanceMode: schemaOrg + "OfflineEventAttendanceMode", URL: pageURL,
		Location: ldPlace{
			Type: "Place", Name: d.VenueName,
			Address: ldAddress{Type: "PostalAddress", Street: d.VenueAddress, Locality: d.VenueCity},
			Geo:     ldGeo{Type: "GeoCoordinates", Latitude: d.VenueLat, Longitude: d.VenueLng},
		},
		Organizer: ldNamed{Type: "Organization", Name: d.OrganizerName},
	}
	if imageURL != "" {
		ld.Image = []string{imageURL}
	}
	for _, a := range pub.Lineup {
		kind := "PerformingGroup"
		if a.Artist.Type.String == "Person" {
			kind = "Person"
		}
		ld.Performer = append(ld.Performer, ldNamed{Type: kind, Name: a.Artist.Name})
	}
	for _, t := range d.Tiers {
		availability := schemaOrg + "InStock"
		if t.Available == 0 || d.Sale != saleOpen {
			availability = schemaOrg + "SoldOut"
		}
		ld.Offers = append(ld.Offers, ldOffer{
			Type: "Offer", Name: t.Name, Price: decimalPrice(t.Price), Currency: "UAH", Availability: availability, URL: pageURL + "#tickets",
		})
	}
	return ld
}

func decimalPrice(kopecks int64) string {
	return strconv.FormatInt(kopecks/100, 10) + "." + pad2(kopecks%100)
}

func metaDescription(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= metaDescriptionLen {
		return text
	}
	runes := []rune(text)[:metaDescriptionLen]
	cut := string(runes)
	if i := strings.LastIndex(cut, " "); i > metaDescriptionLen/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:—–-") + "…"
}

const sitemapEventLimit = 10000

var robotsDisallow = []string{"/org", "/profile", "/orders", "/tickets", "/auth", "/api", "/events/*/buy"}

type sitemapLink struct {
	Rel      string `xml:"rel,attr"`
	Hreflang string `xml:"hreflang,attr"`
	Href     string `xml:"href,attr"`
}

type sitemapURL struct {
	Loc     string        `xml:"loc"`
	LastMod string        `xml:"lastmod,omitempty"`
	Links   []sitemapLink `xml:"xhtml:link"`
}

type sitemapSet struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	XHTML   string       `xml:"xmlns:xhtml,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type SEO struct {
	View   *view.Renderer
	Events *events.Service
	Log    *slog.Logger
}

func (c SEO) Sitemap(w http.ResponseWriter, r *http.Request) {
	rows, err := c.Events.Upcoming(r.Context(), time.Now(), sitemapEventLimit)
	if err != nil {
		c.Log.ErrorContext(r.Context(), "sitemap failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	set := sitemapSet{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9", XHTML: "http://www.w3.org/1999/xhtml"}
	set.URLs = append(set.URLs, c.localized("/", "")...)
	for _, row := range rows {
		set.URLs = append(set.URLs, c.localized("/events/"+row.Event.Slug, row.Event.UpdatedAt.UTC().Format("2006-01-02"))...)
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(set); err != nil {
		c.Log.ErrorContext(r.Context(), "sitemap encode failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	}
}

func (c SEO) localized(path, lastMod string) []sitemapURL {
	links := make([]sitemapLink, 0, len(i18n.Supported)+1)
	for _, lang := range i18n.Supported {
		links = append(links, sitemapLink{Rel: "alternate", Hreflang: string(lang), Href: c.View.Absolute(i18n.Path(lang, path))})
	}
	links = append(links, sitemapLink{Rel: "alternate", Hreflang: "x-default", Href: c.View.Absolute(i18n.Path(i18n.Default, path))})
	out := make([]sitemapURL, 0, len(i18n.Supported))
	for _, lang := range i18n.Supported {
		out = append(out, sitemapURL{Loc: c.View.Absolute(i18n.Path(lang, path)), LastMod: lastMod, Links: links})
	}
	return out
}

func (c SEO) Robots(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	for _, lang := range i18n.Supported {
		for _, p := range robotsDisallow {
			b.WriteString("Disallow: " + strings.TrimSuffix(i18n.Path(lang, p), "/") + "\n")
		}
	}
	b.WriteString("\nSitemap: " + c.View.Absolute("/sitemap.xml") + "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, b.String())
}
