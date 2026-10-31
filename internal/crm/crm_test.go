package crm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHubSpotClientUpsertContact(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody hubspotUpsertBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewHubSpotClientWith("test-token", srv.URL, srv.Client())
	err := c.UpsertContact(context.Background(), Contact{Email: "buyer@example.com", Name: "Buyer", Role: RoleBuyer})
	if err != nil {
		t.Fatalf("UpsertContact() error = %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if !strings.Contains(gotPath, "/crm/v3/objects/contacts/buyer@example.com") || !strings.Contains(gotPath, "idProperty=email") {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", gotAuth)
	}
	if gotBody.Properties.Email != "buyer@example.com" || gotBody.Properties.FirstName != "Buyer" || gotBody.Properties.TixoRole != RoleBuyer {
		t.Errorf("body properties = %+v", gotBody.Properties)
	}
}

func TestHubSpotClientUpsertContactErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewHubSpotClientWith("test-token", srv.URL, srv.Client())
	if err := c.UpsertContact(context.Background(), Contact{Email: "buyer@example.com", Role: RoleBuyer}); err == nil {
		t.Fatal("expected error on 429 response, got nil")
	}
}

func TestDryRunClientUpsertContactDoesNotCallNetwork(t *testing.T) {
	var buf strings.Builder
	d := &DryRunClient{Log: slog.New(slog.NewTextHandler(&buf, nil))}

	if err := d.UpsertContact(context.Background(), Contact{Email: "organizer@example.com", Role: RoleOrganizer}); err != nil {
		t.Fatalf("UpsertContact() error = %v", err)
	}
	if !strings.Contains(buf.String(), "organizer@example.com") || !strings.Contains(buf.String(), RoleOrganizer) {
		t.Errorf("log output = %q, want it to mention email and role", buf.String())
	}
}
