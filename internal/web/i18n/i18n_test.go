package i18n

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/KotunOleg/Tixo/web"
)

func testFS(uk, en string) fstest.MapFS {
	return fstest.MapFS{
		"locales/uk.toml": {Data: []byte(uk)},
		"locales/en.toml": {Data: []byte(en)},
	}
}

const ukTickets = `
[tickets]
one = "{{.Count}} квиток"
few = "{{.Count}} квитки"
many = "{{.Count}} квитків"
other = "{{.Count}} квитка"

[hello]
other = "Привіт, {{.Name}}"
`

const enTickets = `
[tickets]
one = "{{.Count}} ticket"
other = "{{.Count}} tickets"

[hello]
other = "Hello, {{.Name}}"
`

func TestUkrainianPlurals(t *testing.T) {
	tr, err := New(testFS(ukTickets, enTickets))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		lang  Lang
		count int
		want  string
	}{
		{UK, 1, "1 квиток"},
		{UK, 3, "3 квитки"},
		{UK, 5, "5 квитків"},
		{UK, 11, "11 квитків"},
		{UK, 21, "21 квиток"},
		{UK, 22, "22 квитки"},
		{EN, 1, "1 ticket"},
		{EN, 5, "5 tickets"},
	}
	for _, tc := range cases {
		got, err := tr.N(tc.lang, "tickets", tc.count)
		if err != nil {
			t.Fatalf("%s %d: %v", tc.lang, tc.count, err)
		}
		if got != tc.want {
			t.Errorf("%s %d = %q, want %q", tc.lang, tc.count, got, tc.want)
		}
	}
}

func TestTemplateData(t *testing.T) {
	tr, err := New(testFS(ukTickets, enTickets))
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.T(EN, "hello", "Name", "Olena")
	if err != nil || got != "Hello, Olena" {
		t.Errorf("T = %q, %v", got, err)
	}
}

func TestMissingKeyIsAnErrorNotARawID(t *testing.T) {
	tr, err := New(testFS(ukTickets, enTickets))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.T(UK, "nav.nonexistent"); err == nil {
		t.Error("missing key returned no error; the page would silently show a raw message id")
	}
}

func TestKeyMismatchFailsAtStartup(t *testing.T) {
	_, err := New(testFS(ukTickets+"\n[only_uk]\nother = \"лише українською\"\n", enTickets))
	if err == nil || !strings.Contains(err.Error(), "only_uk") {
		t.Errorf("err = %v, want startup error naming only_uk", err)
	}
}

func TestShippedLocalesLoadWithMatchingKeys(t *testing.T) {
	if _, err := New(web.FS); err != nil {
		t.Fatalf("shipped locales are broken: %v", err)
	}
}

func TestMiddlewareLanguageFromPath(t *testing.T) {
	cases := []struct {
		path     string
		wantLang Lang
		wantPath string
	}{
		{"/", UK, "/"},
		{"/events/jazz", UK, "/events/jazz"},
		{"/en", EN, "/"},
		{"/en/", EN, "/"},
		{"/en/events/jazz", EN, "/events/jazz"},
		{"/english-club", UK, "/english-club"},
		{"/events/en", UK, "/events/en"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			var gotLang Lang
			var gotPath string
			h := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				gotLang = FromContext(r.Context())
				gotPath = r.URL.Path
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))
			if gotLang != tc.wantLang || gotPath != tc.wantPath {
				t.Errorf("got (%s, %q), want (%s, %q)", gotLang, gotPath, tc.wantLang, tc.wantPath)
			}
		})
	}
}

func TestLocalizedPath(t *testing.T) {
	cases := []struct {
		lang Lang
		path string
		want string
	}{
		{UK, "/", "/"},
		{UK, "/events", "/events"},
		{EN, "/", "/en/"},
		{EN, "/events", "/en/events"},
	}
	for _, tc := range cases {
		if got := Path(tc.lang, tc.path); got != tc.want {
			t.Errorf("Path(%s, %q) = %q, want %q", tc.lang, tc.path, got, tc.want)
		}
	}
}

func TestSuggestEnglish(t *testing.T) {
	cases := []struct {
		name           string
		lang           Lang
		acceptLanguage string
		dismissed      bool
		want           bool
	}{
		{"english browser on ukrainian page", UK, "en-US,en;q=0.9", false, true},
		{"ukrainian browser", UK, "uk-UA,uk;q=0.9,en;q=0.5", false, false},
		{"russian browser falls back to ukrainian default", UK, "ru-RU,ru;q=0.9", false, false},
		{"already on english page", EN, "en-US", false, false},
		{"user dismissed the banner", UK, "en-US", true, false},
		{"no header", UK, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tc.acceptLanguage != "" {
				r.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			if tc.dismissed {
				r.AddCookie(&http.Cookie{Name: DismissCookie, Value: "1"})
			}
			if got := SuggestEnglish(r, tc.lang); got != tc.want {
				t.Errorf("SuggestEnglish = %v, want %v", got, tc.want)
			}
		})
	}
}
