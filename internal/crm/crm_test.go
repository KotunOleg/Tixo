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
	var gotBody hubspotBatchUpsertBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewHubSpotClientWith("test-token", srv.URL, srv.Client())
	err := c.UpsertContact(context.Background(), Contact{Email: "buyer@example.com", Name: "Buyer", Role: RoleBuyer})
	if err != nil {
		t.Fatalf("UpsertContact() error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/crm/v3/objects/contacts/batch/upsert" {
		t.Errorf("path = %q, want the batch upsert endpoint", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", gotAuth)
	}
	if len(gotBody.Inputs) != 1 {
		t.Fatalf("inputs = %d, want 1", len(gotBody.Inputs))
	}
	in := gotBody.Inputs[0]
	if in.IDProperty != "email" || in.ID != "buyer@example.com" {
		t.Errorf("input id = %+v, want idProperty=email id=buyer@example.com", in)
	}
	if in.Properties.Email != "buyer@example.com" || in.Properties.FirstName != "Buyer" || in.Properties.TixoRole != RoleBuyer {
		t.Errorf("body properties = %+v", in.Properties)
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

func TestHubSpotClientUpsertContactCreatesBrandNewContact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/crm/v3/objects/contacts/batch/upsert" {
			t.Errorf("a non-existent contact must go through the batch upsert endpoint, not single-object PATCH: got %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewHubSpotClientWith("test-token", srv.URL, srv.Client())
	if err := c.UpsertContact(context.Background(), Contact{Email: "brand-new@example.com", Role: RoleBuyer}); err != nil {
		t.Fatalf("UpsertContact() for a contact with no existing HubSpot record: error = %v, want success", err)
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
