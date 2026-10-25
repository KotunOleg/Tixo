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
	"github.com/KotunOleg/Tixo/web"
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
	v.Render(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), http.StatusOK, "pages/page", nil)
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

func TestPagePurchasePopsOnce(t *testing.T) {
	calls := 0
	p := &Page{popPurchase: func() (string, bool) {
		calls++
		return `{"transaction_id":"1"}`, true
	}}
	if got := p.Purchase(); got != `{"transaction_id":"1"}` {
		t.Errorf("Purchase() = %q, want the payload", got)
	}
	if got := p.Purchase(); got != `{"transaction_id":"1"}` {
		t.Errorf("second Purchase() on the same Page = %q, want the cached payload", got)
	}
	if calls != 1 {
		t.Errorf("popPurchase called %d times, want 1: a page render must pop the session value at most once", calls)
	}
}

func TestRenderExposesGA4IDAndPurchasePayloadOnlyWhenPresent(t *testing.T) {
	fsys := fixture(`{{define "content"}}GA4=[{{.GA4ID}}] <script type="application/json" id="p">{{.Purchase}}</script>{{end}}`)
	tr, err := i18n.New(fsys)
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(fsys, tr, Config{BaseURL: url.URL{Scheme: "http", Host: "x"}, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), GA4ID: "G-TEST"})
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithPurchase(t.Context(), func() (string, bool) { return `{"transaction_id":"1"}`, true })
	rec := httptest.NewRecorder()
	v.Render(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil), http.StatusOK, "pages/page", nil)
	if body := rec.Body.String(); !strings.Contains(body, "GA4=[G-TEST]") || !strings.Contains(body, `id="p">{"transaction_id":"1"}</script>`) {
		t.Errorf("body = %q, want GA4ID and the unescaped purchase JSON inside the script tag", body)
	}

	rec = httptest.NewRecorder()
	v.Render(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), http.StatusOK, "pages/page", nil)
	if body := rec.Body.String(); !strings.Contains(body, `id="p"></script>`) {
		t.Errorf("body = %q, want an empty purchase script without WithPurchase in context", body)
	}
}

func TestGA4ScriptLoadsOnlyWithMeasurementID(t *testing.T) {
	tr, err := i18n.New(web.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		ga4ID string
		want  bool
	}{
		{"disabled by default", "", false},
		{"enabled with an id", "G-TEST123", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := New(web.FS, tr, Config{BaseURL: url.URL{Scheme: "http", Host: "x"}, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), GA4ID: tc.ga4ID})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			v.NotFound(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/no-such-page", nil))
			body := rec.Body.String()
			hasScript := strings.Contains(body, "googletagmanager.com/gtag/js")
			if hasScript != tc.want {
				t.Errorf("gtag script present = %v, want %v\n%s", hasScript, tc.want, body)
			}
			if tc.want && !strings.Contains(body, `gtag("config","G-TEST123")`) {
				t.Errorf("gtag not configured with the measurement id:\n%s", body)
			}
		})
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
