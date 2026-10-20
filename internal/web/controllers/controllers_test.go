package controllers

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/server"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
	"github.com/KotunOleg/Tixo/web"
)

func newSite(t *testing.T, extra func(r chi.Router)) http.Handler {
	t.Helper()
	tr, err := i18n.New(web.FS)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	v, err := view.New(web.FS, tr, view.Config{BaseURL: url.URL{Scheme: "https", Host: "tixo.example"}, Logger: log})
	if err != nil {
		t.Fatalf("templates failed to parse: %v", err)
	}
	return server.New(server.Deps{
		Logger: log,
		Health: http.NotFoundHandler(),
		Routes: func(r chi.Router) {
			Mount(r, v, web.FS)
			if extra != nil {
				extra(r)
			}
		},
		NotFound:    v.NotFound,
		ServerError: v.ServerError,
	})
}

func do(h http.Handler, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
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
			rec := do(site, tc.path)
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
	body := do(newSite(t, nil), "/en/").Body.String()
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
	if body := do(site, "/").Body.String(); !strings.Contains(body, `href="/en/"`) {
		t.Error("ukrainian home has no switch link to /en/")
	}
	if body := do(site, "/en/").Body.String(); !strings.Contains(body, `href="/" hreflang="uk"`) {
		t.Error("english home has no switch link back to /")
	}
}

func TestEnglishSuggestionBanner(t *testing.T) {
	site := newSite(t, nil)
	marker := `id="lang-suggest"`
	if body := do(site, "/", "Accept-Language", "en-US,en;q=0.9").Body.String(); !strings.Contains(body, marker) {
		t.Error("english browser on ukrainian page sees no suggestion banner")
	}
	if body := do(site, "/", "Accept-Language", "uk-UA").Body.String(); strings.Contains(body, marker) {
		t.Error("ukrainian browser is offered english")
	}
	if body := do(site, "/en/", "Accept-Language", "en-US").Body.String(); strings.Contains(body, marker) {
		t.Error("banner shown on the english page itself")
	}
}

func TestPanicRendersErrorPageWithRequestID(t *testing.T) {
	site := newSite(t, func(r chi.Router) {
		r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
	rec := do(site, "/boom")
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
	}
	for _, tc := range cases {
		rec := do(site, tc.path)
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
	if rec := do(site, "/static/"); rec.Code != http.StatusNotFound {
		t.Errorf("/static/ directory listing exposed: %d", rec.Code)
	}
}
