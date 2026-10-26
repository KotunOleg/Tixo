package controllers

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2/memstore"
	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/server"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
	"github.com/KotunOleg/Tixo/web"
)

var testBaseURL = url.URL{Scheme: "https", Host: "tixo.example"}

func newSite(t *testing.T, extra func(r chi.Router)) http.Handler {
	t.Helper()
	h, _ := buildSite(t, nil, extra)
	return h
}

func buildSite(t *testing.T, configure func(d *Deps), extra func(r chi.Router)) (http.Handler, Deps) {
	t.Helper()
	tr, err := i18n.New(web.FS)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := Deps{FS: web.FS, Logger: log}
	if configure != nil {
		configure(&d)
	}
	v, err := view.New(web.FS, tr, view.Config{BaseURL: testBaseURL, Logger: log, SignIn: d.Google != nil})
	if err != nil {
		t.Fatalf("templates failed to parse: %v", err)
	}
	d.View = v
	d.Sessions = auth.NewSessionsWithStore(memstore.NewWithCleanupInterval(0), testBaseURL, log, v.ServerError)
	return server.New(server.Deps{
		Logger: log,
		Health: http.NotFoundHandler(),
		Routes: func(r chi.Router) {
			Mount(r, d)
			if extra != nil {
				extra(r)
			}
		},
		NotFound:    d.NotFound(),
		ServerError: v.ServerError,
	}), d
}

func do(t *testing.T, h http.Handler, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPagesRenderInBothLanguages(t *testing.T) {
	site := newSite(t, nil)
	cases := []struct {
		path       string
		wantStatus int
		wantLang   string
		wantText   string
	}{
		{"/", http.StatusOK, "uk", "Знайди свою подію"},
		{"/en/", http.StatusOK, "en", "Find your event"},
		{"/no-such-page", http.StatusNotFound, "uk", "Сторінку не знайдено"},
		{"/en/no-such-page", http.StatusNotFound, "en", "Page not found"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := do(t, site, tc.path)
			body := rec.Body.String()
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d\n%s", rec.Code, tc.wantStatus, body)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q", ct)
			}
			if !strings.Contains(body, `<html lang="`+tc.wantLang+`"`) {
				t.Errorf("missing <html lang=%q>", tc.wantLang)
			}
			if !strings.Contains(body, tc.wantText) {
				t.Errorf("missing %q", tc.wantText)
			}
			if !strings.Contains(body, `class="navbar`) || !strings.Contains(body, "<footer") {
				t.Error("page is not wrapped in the site layout")
			}
			for _, bad := range []string{"<no value>", "{{", "%!"} {
				if strings.Contains(body, bad) {
					t.Errorf("rendered page contains template artefact %q", bad)
				}
			}
		})
	}
}

func TestSEOLinks(t *testing.T) {
	body := do(t, newSite(t, nil), "/en/").Body.String()
	for _, want := range []string{
		`<link rel="canonical" href="https://tixo.example/en/">`,
		`<link rel="alternate" hreflang="uk" href="https://tixo.example/">`,
		`<link rel="alternate" hreflang="en" href="https://tixo.example/en/">`,
		`<link rel="alternate" hreflang="x-default" href="https://tixo.example/">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestLanguageSwitchKeepsCurrentPage(t *testing.T) {
	site := newSite(t, nil)
	if body := do(t, site, "/").Body.String(); !strings.Contains(body, `href="/en/"`) {
		t.Error("ukrainian home has no switch link to /en/")
	}
	if body := do(t, site, "/en/").Body.String(); !strings.Contains(body, `href="/" hreflang="uk"`) {
		t.Error("english home has no switch link back to /")
	}
}

func TestLinksNeverLeaveSiteForCraftedPaths(t *testing.T) {
	site := newSite(t, nil)
	hrefs := regexp.MustCompile(`href="([^"]*)"`)
	for _, path := range []string{
		"//evil.example/x",
		"/en//evil.example/x",
		"/en/%2F%2Fevil.example",
		"/en/%5Cevil.example",
		"/en/%5C%5Cevil.example",
		"/en///evil.example",
	} {
		t.Run(path, func(t *testing.T) {
			for _, m := range hrefs.FindAllStringSubmatch(do(t, site, path).Body.String(), -1) {
				href := m[1]
				if strings.HasPrefix(href, "//") || strings.HasPrefix(href, "/\\") {
					t.Errorf("link points off-site: %s", href)
				}
			}
		})
	}
}

func TestEnglishSuggestionBanner(t *testing.T) {
	site := newSite(t, nil)
	marker := `id="lang-suggest"`
	if body := do(t, site, "/", "Accept-Language", "en-US,en;q=0.9").Body.String(); !strings.Contains(body, marker) {
		t.Error("english browser on ukrainian page sees no suggestion banner")
	}
	if body := do(t, site, "/", "Accept-Language", "uk-UA").Body.String(); strings.Contains(body, marker) {
		t.Error("ukrainian browser is offered english")
	}
	if body := do(t, site, "/en/", "Accept-Language", "en-US").Body.String(); strings.Contains(body, marker) {
		t.Error("banner shown on the english page itself")
	}
}

func TestPanicRendersErrorPageWithRequestID(t *testing.T) {
	site := newSite(t, func(r chi.Router) {
		r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
	rec := do(t, site, "/boom")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<footer") {
		t.Error("500 page is not wrapped in the layout")
	}
	if id := rec.Header().Get("X-Request-Id"); id == "" || !strings.Contains(body, id) {
		t.Error("500 page does not show the request id the user can report")
	}
}

func TestStaticAssets(t *testing.T) {
	site := newSite(t, nil)
	cases := []struct {
		path        string
		contentType string
	}{
		{"/static/css/app.css", "text/css"},
		{"/static/js/app.js", "text/javascript"},
		{"/static/img/logo.svg", "image/svg+xml"},
		{"/static/fonts/inter-cyrillic-wght-normal.woff2", "font/woff2"},
		{"/static/fonts/unbounded-cyrillic-wght-normal.woff2", "font/woff2"},
	}
	for _, tc := range cases {
		rec := do(t, site, tc.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d", tc.path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.contentType) {
			t.Errorf("%s Content-Type = %q, want %s", tc.path, ct, tc.contentType)
		}
		if rec.Header().Get("Cache-Control") == "" {
			t.Errorf("%s has no Cache-Control", tc.path)
		}
	}
	for _, path := range []string{"/static/", "/static/css/", "/static/%2e%2e/templates/layouts/base.html", "/static/..%2ftemplates/layouts/base.html"} {
		rec := do(t, site, path)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), `{{define "base"}}`) {
			t.Errorf("%s escaped the static dir or listed it: %d", path, rec.Code)
		}
	}
}
