package features

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func mustOrganizer(t *testing.T, pool *pgxpool.Pool, slug, plan string) int64 {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(pool)
	org, err := q.CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: slug, Name: slug, Contacts: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetOrganizerPlan(ctx, dbgen.SetOrganizerPlanParams{ID: org.ID, Plan: plan}); err != nil {
		t.Fatal(err)
	}
	return org.ID
}

func TestEnabledCombinesPlanOverrideAndGlobalSwitch(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	svc := New(pool)
	premium := mustOrganizer(t, pool, "kyiv-live", "premium")
	free := mustOrganizer(t, pool, "lviv-sound", "free")
	freeWithOverride := mustOrganizer(t, pool, "odesa-jazz", "free")
	if err := svc.Enable(ctx, freeWithOverride, SalesAnalytics); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO feature_flags (name, enabled_for_all) VALUES ('everyone_gets_it', true), ('nobody_gets_it', false)"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		org  int64
		flag string
		want bool
	}{
		{"premium plan grants analytics", premium, SalesAnalytics, true},
		{"free plan does not grant analytics", free, SalesAnalytics, false},
		{"override grants analytics to a free organizer", freeWithOverride, SalesAnalytics, true},
		{"enabled_for_all reaches a free organizer", free, "everyone_gets_it", true},
		{"flag with no plans and no override stays off even for premium", premium, "nobody_gets_it", false},
	}
	for _, tc := range cases {
		got, err := svc.Enabled(ctx, tc.org, tc.flag)
		if err != nil || got != tc.want {
			t.Errorf("%s: Enabled = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestUnknownFlagIsAnErrorNotSilentlyOff(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	svc := New(pool)
	org := mustOrganizer(t, pool, "kyiv-live", "premium")

	if _, err := svc.Enabled(ctx, org, "sales_analytcs"); !errors.Is(err, ErrUnknown) {
		t.Errorf("Enabled(typo) err = %v, want ErrUnknown", err)
	}
	if err := svc.Enable(ctx, org, "sales_analytcs"); !errors.Is(err, ErrUnknown) {
		t.Errorf("Enable(typo) err = %v, want ErrUnknown", err)
	}
	if err := svc.Disable(ctx, org, "sales_analytcs"); !errors.Is(err, ErrUnknown) {
		t.Errorf("Disable(typo) err = %v, want ErrUnknown", err)
	}
	if _, err := svc.Enabled(ctx, org+1000, SalesAnalytics); !errors.Is(err, ErrUnknown) {
		t.Errorf("Enabled(missing organizer) err = %v, want ErrUnknown", err)
	}
}

func TestToggleWithoutChangingPlan(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	svc := New(pool)
	free := mustOrganizer(t, pool, "lviv-sound", "free")

	for i := 0; i < 2; i++ {
		if err := svc.Enable(ctx, free, SalesAnalytics); err != nil {
			t.Fatalf("Enable #%d: %v", i+1, err)
		}
	}
	if on, err := svc.Enabled(ctx, free, SalesAnalytics); err != nil || !on {
		t.Fatalf("after enable: %v, %v", on, err)
	}
	if err := svc.Disable(ctx, free, SalesAnalytics); err != nil {
		t.Fatal(err)
	}
	if on, err := svc.Enabled(ctx, free, SalesAnalytics); err != nil || on {
		t.Errorf("after disable: %v, %v; want off", on, err)
	}
	var plan string
	if err := pool.QueryRow(ctx, "SELECT plan FROM organizers WHERE id = $1", free).Scan(&plan); err != nil || plan != "free" {
		t.Errorf("plan after toggling = %q, %v; want free", plan, err)
	}
}

func TestListExplainsWhyAFeatureIsOn(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	svc := New(pool)
	premium := mustOrganizer(t, pool, "kyiv-live", "premium")
	if err := svc.Enable(ctx, premium, SalesAnalytics); err != nil {
		t.Fatal(err)
	}

	states, err := svc.List(ctx, premium)
	if err != nil {
		t.Fatal(err)
	}
	want := State{Name: SalesAnalytics, ByPlan: true, ByOverride: true}
	if len(states) != 1 || states[0] != want {
		t.Errorf("List = %+v, want [%+v]", states, want)
	}
}
