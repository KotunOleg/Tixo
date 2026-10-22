package httpx

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestPaginate(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		wantSkip  int32
		wantLimit int32
		wantErr   bool
	}{
		{"defaults", "", 0, DefaultLimit, false},
		{"explicit values", "skip=40&limit=10", 40, 10, false},
		{"limit above max is clamped", "limit=1000", 0, MaxLimit, false},
		{"limit at max stays", "limit=100", 0, 100, false},
		{"zero limit is valid", "limit=0", 0, 0, false},
		{"negative skip is invalid", "skip=-1", 0, 0, true},
		{"negative limit is invalid", "limit=-1", 0, 0, true},
		{"non numeric skip is invalid", "skip=abc", 0, 0, true},
		{"non numeric limit is invalid", "limit=abc", 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/events?"+c.query, nil)
			skip, limit, err := Paginate(r)
			if c.wantErr {
				if !errors.Is(err, ErrInvalidPagination) {
					t.Fatalf("err = %v, want ErrInvalidPagination", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if skip != c.wantSkip || limit != c.wantLimit {
				t.Fatalf("skip,limit = %d,%d want %d,%d", skip, limit, c.wantSkip, c.wantLimit)
			}
		})
	}
}

func TestEnvelopeNextLink(t *testing.T) {
	cases := []struct {
		name     string
		skip     int32
		limit    int32
		count    int64
		wantNext string
	}{
		{"first page of more", 0, 20, 45, "/api/v1/events?limit=20&skip=20"},
		{"last page", 40, 20, 45, ""},
		{"exact last item", 25, 20, 45, ""},
		{"empty result", 0, 20, 0, ""},
		{"zero limit never loops back to itself", 0, 0, 45, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/events", nil)
			page := Envelope([]int{}, c.count, c.skip, c.limit, r)
			switch {
			case c.wantNext == "" && page.NextLink != nil:
				t.Fatalf("nextLink = %q, want nil", *page.NextLink)
			case c.wantNext != "" && (page.NextLink == nil || *page.NextLink != c.wantNext):
				got := "nil"
				if page.NextLink != nil {
					got = *page.NextLink
				}
				t.Fatalf("nextLink = %s, want %s", got, c.wantNext)
			}
		})
	}
}

func TestEnvelopeReplacesNilSliceWithEmpty(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/artists", nil)
	var items []int
	page := Envelope(items, 0, 0, 20, r)
	got, ok := page.Items.([]int)
	if !ok || got == nil {
		t.Fatalf("items = %#v, want a non-nil empty slice", page.Items)
	}
	if len(got) != 0 {
		t.Fatalf("items = %v, want empty", got)
	}
}
