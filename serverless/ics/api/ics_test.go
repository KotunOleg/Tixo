package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func validQuery() string {
	return "title=Big+Show&start=2026-11-05T19:00:00%2B02:00&end=2026-11-05T21:00:00%2B02:00" +
		"&location=Kyiv+Arena&description=Live+concert&url=https://tixo.me/events/big-show"
}

func TestHandlerReturnsValidICSForValidInput(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ics?"+validQuery(), nil)
	rec := httptest.NewRecorder()

	Handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q, want text/calendar", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="event.ics"`) {
		t.Errorf("Content-Disposition = %q, want filename=event.ics", cd)
	}

	body := rec.Body.String()
	for _, want := range []string{"BEGIN:VCALENDAR", "BEGIN:VEVENT", "SUMMARY:Big Show", "DTSTART:20261105T170000Z",
		"DTEND:20261105T190000Z", "LOCATION:Kyiv Arena", "END:VEVENT", "END:VCALENDAR"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body:\n%s", want, body)
		}
	}
}

func TestHandlerIsDeterministicUIDForTheSameInput(t *testing.T) {
	req1 := httptest.NewRequest(http.MethodGet, "/api/ics?"+validQuery(), nil)
	rec1 := httptest.NewRecorder()
	Handler(rec1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/api/ics?"+validQuery(), nil)
	rec2 := httptest.NewRecorder()
	Handler(rec2, req2)

	uid1 := extractLine(t, rec1.Body.String(), "UID:")
	uid2 := extractLine(t, rec2.Body.String(), "UID:")
	if uid1 != uid2 {
		t.Errorf("UID changed across identical requests: %q != %q", uid1, uid2)
	}
}

func extractLine(t *testing.T, body, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\r\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no line with prefix %q in body:\n%s", prefix, body)
	return ""
}

func TestHandlerRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing title", "start=2026-11-05T19:00:00Z&end=2026-11-05T21:00:00Z"},
		{"blank title", "title=+&start=2026-11-05T19:00:00Z&end=2026-11-05T21:00:00Z"},
		{"malformed start", "title=X&start=not-a-date&end=2026-11-05T21:00:00Z"},
		{"malformed end", "title=X&start=2026-11-05T19:00:00Z&end=not-a-date"},
		{"end before start", "title=X&start=2026-11-05T21:00:00Z&end=2026-11-05T19:00:00Z"},
		{"end equal start", "title=X&start=2026-11-05T19:00:00Z&end=2026-11-05T19:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/ics?"+tc.query, nil)
			rec := httptest.NewRecorder()
			Handler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestEscapeTextEscapesSpecialCharacters(t *testing.T) {
	got := escapeText("Rock, Pop; \\Weekend\\\nSecond line")
	want := `Rock\, Pop\; \\Weekend\\\nSecond line`
	if got != want {
		t.Errorf("escapeText() = %q, want %q", got, want)
	}
}

func TestEscapeTextEscapesBareCarriageReturn(t *testing.T) {
	got := escapeText("Evil\rEND:VEVENT\rBEGIN:VALARM")
	if strings.Contains(got, "\r") {
		t.Errorf("escapeText() left a bare CR in %q — a lenient parser could read it as a line break and inject calendar content", got)
	}
}
