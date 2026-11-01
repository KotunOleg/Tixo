package main

import (
	"testing"
	"time"
)

func TestParseRemindArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    time.Duration
		wantErr bool
	}{
		{"default is a day", nil, 24 * time.Hour, false},
		{"demo window", []string{"--within=72h"}, 72 * time.Hour, false},
		{"zero would queue nothing silently", []string{"--within=0"}, 0, true},
		{"negative", []string{"--within=-1h"}, 0, true},
		{"not a duration", []string{"--within=3"}, 0, true},
		{"stray argument", []string{"now"}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRemindArgs(tc.args)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("parseRemindArgs(%v) = %s, %v; want %s, error %v", tc.args, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
