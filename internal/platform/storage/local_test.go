package storage

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSaveOpenRoundTrip(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.Save(strings.NewReader("poster bytes"), "jpg")
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "poster bytes" {
		t.Errorf("content = %q, want %q", got, "poster bytes")
	}
}

func TestSaveGeneratesDistinctKeys(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.Save(strings.NewReader("a"), "png")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Save(strings.NewReader("b"), "png")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two uploads got the same key %q, second overwrote the first", a)
	}
}

func TestOpenRejectsPathTraversal(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open("../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open(traversal key) = %v, want ErrNotFound", err)
	}
}

func TestOpenUnknownKeyNotFound(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open("0123456789abcdef0123456789abcdef.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open(unknown key) = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.Save(strings.NewReader("poster"), "webp")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open after Delete = %v, want ErrNotFound", err)
	}
}

func TestDeleteRejectsPathTraversal(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("../../etc/passwd"); err == nil {
		t.Error("Delete accepted a path-traversal key")
	}
}

func TestDeleteMissingKeyIsNoop(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("0123456789abcdef0123456789abcdef.jpg"); err != nil {
		t.Errorf("Delete(missing key) = %v, want nil", err)
	}
}
