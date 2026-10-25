package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/features"
)

func TestParseFlagsArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    flagsArgs
		wantErr string
	}{
		{"org after the flag name", []string{"enable", "sales_analytics", "--org", "lviv-sound"}, flagsArgs{"enable", "sales_analytics", "lviv-sound"}, ""},
		{"org before the flag name", []string{"disable", "--org=lviv-sound", "sales_analytics"}, flagsArgs{"disable", "sales_analytics", "lviv-sound"}, ""},
		{"list needs only org", []string{"list", "--org", "kyiv-live"}, flagsArgs{"list", "", "kyiv-live"}, ""},
		{"no action", nil, flagsArgs{}, "usage"},
		{"missing org would toggle nothing", []string{"enable", "sales_analytics"}, flagsArgs{}, "--org is required"},
		{"missing flag name", []string{"enable", "--org", "x"}, flagsArgs{}, "exactly one flag name"},
		{"two flag names", []string{"enable", "a", "b", "--org", "x"}, flagsArgs{}, "exactly one flag name"},
		{"list with a stray name", []string{"list", "a", "--org", "x"}, flagsArgs{}, "unexpected"},
		{"unknown action", []string{"toggle", "a", "--org", "x"}, flagsArgs{}, "unknown action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFlagsArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestFlagsCommandSwitchesAnalyticsForAFreeOrganizer(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	org, err := dbgen.New(pool).CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: "lviv-sound", Name: "Lviv Sound", Contacts: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	svc := features.New(pool)

	steps := []struct {
		action  string
		wantOn  bool
		wantOut string
	}{
		{"enable", true, "lviv-sound sales_analytics on (override)\n"},
		{"disable", false, "lviv-sound sales_analytics off\n"},
	}
	for _, s := range steps {
		var out bytes.Buffer
		if err := runFlags(ctx, pool, flagsArgs{action: s.action, name: features.SalesAnalytics, org: "lviv-sound"}, &out); err != nil {
			t.Fatalf("%s: %v", s.action, err)
		}
		if out.String() != s.wantOut {
			t.Errorf("%s output = %q, want %q", s.action, out.String(), s.wantOut)
		}
		if on, err := svc.Enabled(ctx, org.ID, features.SalesAnalytics); err != nil || on != s.wantOn {
			t.Errorf("after %s: Enabled = %v, %v; want %v", s.action, on, err, s.wantOn)
		}
	}
}

func TestFlagsCommandRejectsUnknownOrganizerAndFlag(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	if _, err := dbgen.New(pool).CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: "lviv-sound", Name: "Lviv Sound", Contacts: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		a       flagsArgs
		wantErr string
	}{
		{flagsArgs{"enable", features.SalesAnalytics, "no-such-org"}, "not found"},
		{flagsArgs{"enable", "sales_analytcs", "lviv-sound"}, "unknown feature flag"},
	}
	for _, tc := range cases {
		err := runFlags(ctx, pool, tc.a, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("runFlags(%+v) err = %v, want %q", tc.a, err, tc.wantErr)
		}
	}
}
