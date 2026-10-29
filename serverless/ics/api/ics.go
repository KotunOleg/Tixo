package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	dtFormat  = "20060102T150405Z"
	uidDomain = "tixo.me"
)

type eventParams struct {
	Title       string
	Start       time.Time
	End         time.Time
	Location    string
	Description string
	URL         string
}

func parseParams(q url.Values) (eventParams, error) {
	title := strings.TrimSpace(q.Get("title"))
	if title == "" {
		return eventParams{}, fmt.Errorf("title is required")
	}

	start, err := time.Parse(time.RFC3339, q.Get("start"))
	if err != nil {
		return eventParams{}, fmt.Errorf("start must be an RFC3339 timestamp: %w", err)
	}
	end, err := time.Parse(time.RFC3339, q.Get("end"))
	if err != nil {
		return eventParams{}, fmt.Errorf("end must be an RFC3339 timestamp: %w", err)
	}
	if !end.After(start) {
		return eventParams{}, fmt.Errorf("end must be after start")
	}

	return eventParams{
		Title:       title,
		Start:       start,
		End:         end,
		Location:    strings.TrimSpace(q.Get("location")),
		Description: strings.TrimSpace(q.Get("description")),
		URL:         strings.TrimSpace(q.Get("url")),
	}, nil
}

func (p eventParams) uid() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		p.Title, p.Start.UTC().Format(dtFormat), p.End.UTC().Format(dtFormat), p.Location, p.Description, p.URL,
	}, "\x00")))
	return hex.EncodeToString(sum[:]) + "@" + uidDomain
}

func escapeText(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, "\r\n", `\n`, "\n", `\n`)
	return replacer.Replace(s)
}

func render(p eventParams) string {
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//Tixo//Add to calendar//EN",
		"BEGIN:VEVENT",
		"UID:" + p.uid(),
		"DTSTAMP:" + time.Now().UTC().Format(dtFormat),
		"DTSTART:" + p.Start.UTC().Format(dtFormat),
		"DTEND:" + p.End.UTC().Format(dtFormat),
		"SUMMARY:" + escapeText(p.Title),
	}
	if p.Location != "" {
		lines = append(lines, "LOCATION:"+escapeText(p.Location))
	}
	if p.Description != "" {
		lines = append(lines, "DESCRIPTION:"+escapeText(p.Description))
	}
	if p.URL != "" {
		lines = append(lines, "URL:"+escapeText(p.URL))
	}
	lines = append(lines, "END:VEVENT", "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n"
}

func Handler(w http.ResponseWriter, r *http.Request) {
	params, err := parseParams(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="event.ics"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(render(params)))
}
