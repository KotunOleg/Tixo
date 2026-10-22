package artists

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
)

func TestSearchRequiresMinimumLength(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	if _, err := svc.Create(context.Background(), "Okean Elzy"); err != nil {
		t.Fatal(err)
	}

	rows, err := svc.Search(context.Background(), "O")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("Search(too short) = %+v, want empty", rows)
	}

	rows, err = svc.Search(context.Background(), "Ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("Search(2 chars) = %+v, want empty", rows)
	}

	rows, err = svc.Search(context.Background(), "Oke")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "Okean Elzy" {
		t.Errorf("Search(3 chars) = %+v, want [Okean Elzy]", rows)
	}

	rows, err = svc.Search(context.Background(), "Okean")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "Okean Elzy" {
		t.Errorf("Search(Okean) = %+v, want [Okean Elzy]", rows)
	}
}

func TestCreateAndGet(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	a, err := svc.Create(context.Background(), "  New Band  ")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "New Band" {
		t.Errorf("Create name = %q, want trimmed %q", a.Name, "New Band")
	}

	got, err := svc.Get(context.Background(), a.ID)
	if err != nil || got.ID != a.ID {
		t.Errorf("Get(%d) = %+v, %v", a.ID, got, err)
	}
}

func TestCreateRejectsEmptyAndTooLongName(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	if _, err := svc.Create(context.Background(), "   "); err == nil {
		t.Error("Create(blank) = nil error, want error")
	}
	if _, err := svc.Create(context.Background(), strings.Repeat("a", nameMaxLen+1)); !errors.Is(err, ErrNameTooLong) {
		t.Errorf("Create(too long) = %v, want ErrNameTooLong", err)
	}
}

func TestGetMissingNotFound(t *testing.T) {
	pool := dbtest.Pool(t)
	svc := New(pool)
	if _, err := svc.Get(context.Background(), 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}
