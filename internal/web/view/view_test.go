package view

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

const locale = "[ok]\nother = \"fine\"\n"

func fixture(page string) fstest.MapFS {
	return fstest.MapFS{
		"locales/uk.toml":               {Data: []byte(locale)},
		"locales/en.toml":               {Data: []byte(locale)},
		"templates/layouts/base.html":   {Data: []byte(`{{define "base"}}<html>BEGIN {{template "content" .}} END</html>{{end}}`)},
		"templates/partials/empty.html": {Data: []byte(`{{define "partial"}}{{end}}`)},
		"templates/pages/page.html":     {Data: []byte(page)},
		"templates/errors/404.html":     {Data: []byte(`{{define "content"}}nf{{end}}`)},
		"templates/errors/500.html":     {Data: []byte(`{{define "content"}}err{{end}}`)},
	}
}

func newRenderer(t *testing.T, page string, logs *bytes.Buffer) (*Renderer, error) {
	t.Helper()
	fsys := fixture(page)
	tr, err := i18n.New(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return New(fsys, tr, Config{BaseURL: url.URL{Scheme: "http", Host: "x"}, Logger: slog.New(slog.NewTextHandler(logs, nil))})
}

func TestRenderErrorSendsNoPartialPage(t *testing.T) {
	var logs bytes.Buffer
	v, err := newRenderer(t, `{{define "content"}}{{.T "missing.key"}}{{end}}`, &logs)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	v.Render(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK, "pages/page", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "BEGIN") {
		t.Error("half-rendered page was sent to the client")
	}
	if !strings.Contains(logs.String(), "missing.key") {
		t.Errorf("render error not logged:\n%s", logs.String())
	}
}

func TestBrokenTemplateFailsAtStartup(t *testing.T) {
	if _, err := newRenderer(t, `{{define "content"}}{{if}}{{end}}`, &bytes.Buffer{}); err == nil {
		t.Error("syntax error in a page template was not reported at startup")
	}
}

func TestURLStaysInsideSite(t *testing.T) {
	p := &Page{Lang: i18n.EN}
	cases := map[string]string{
		"/events":          "/en/events",
		"events":           "/en/events",
		"/":                "/en/",
		"/a/../../etc":     "/en/etc",
		"//evil.example/x": "/en/evil.example/x",
	}
	for in, want := range cases {
		if got := p.URL(in); got != want {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
	}
}
