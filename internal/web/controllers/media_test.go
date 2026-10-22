package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/platform/storage"
)

func newMediaSite(t *testing.T) (http.Handler, *storage.Store) {
	t.Helper()
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, _ := buildSite(t, func(d *Deps) { d.Storage = store }, nil)
	return h, store
}

func TestMediaServesUploadedFile(t *testing.T) {
	h, store := newMediaSite(t)
	key, err := store.Save(strings.NewReader("poster bytes"), "jpg")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/"+key, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /media/%s = %d, want 200\n%s", key, rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "poster bytes" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "poster bytes")
	}
}

func TestMediaUnknownKeyNotFound(t *testing.T) {
	h, _ := newMediaSite(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/0123456789abcdef0123456789abcdef.jpg", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /media/<unknown> = %d, want 404", rec.Code)
	}
}

func TestMediaRejectsPathTraversal(t *testing.T) {
	h, store := newMediaSite(t)
	if _, err := store.Save(strings.NewReader("secret"), "txt"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/..%2F..%2Fetc%2Fpasswd", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("GET /media/<traversal> = 200, want not-found/error")
	}
}
