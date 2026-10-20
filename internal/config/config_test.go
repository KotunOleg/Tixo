package config

import (
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{
		"APP_ENV":      "dev",
		"DATABASE_URL": "postgres://tixo:tixo@localhost:5432/tixo?sslmode=disable",
		"REDIS_URL":    "redis://localhost:6379/0",
		"AMQP_URL":     "amqp://guest:guest@localhost:5672/",
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(validEnv())
	if err != nil {
		t.Fatalf("valid env rejected: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.BaseURL.String() != "http://localhost:8080" {
		t.Errorf("BaseURL = %q, want http://localhost:8080", cfg.BaseURL.String())
	}

	if len(cfg.SessionSecret) < minSecretLen {
		t.Errorf("dev session secret not generated: len=%d", len(cfg.SessionSecret))
	}
}

func TestLoadRejects(t *testing.T) {
	cases := []struct {
		name    string
		set     map[string]string
		unset   []string
		wantVar string
	}{
		{name: "missing database url", unset: []string{"DATABASE_URL"}, wantVar: "DATABASE_URL"},
		{name: "missing redis url", unset: []string{"REDIS_URL"}, wantVar: "REDIS_URL"},
		{name: "missing amqp url", unset: []string{"AMQP_URL"}, wantVar: "AMQP_URL"},
		{name: "unknown app env", set: map[string]string{"APP_ENV": "staging"}, wantVar: "APP_ENV"},
		{name: "base url without scheme", set: map[string]string{"BASE_URL": "localhost:8080"}, wantVar: "BASE_URL"},
		{name: "base url with trailing path", set: map[string]string{"BASE_URL": "https://tixo.me/app"}, wantVar: "BASE_URL"},
		{name: "prod without session secret", set: map[string]string{"APP_ENV": "prod", "BASE_URL": "https://tixo.me"}, wantVar: "SESSION_SECRET"},
		{name: "prod with short session secret", set: map[string]string{"APP_ENV": "prod", "BASE_URL": "https://tixo.me", "SESSION_SECRET": "short"}, wantVar: "SESSION_SECRET"},
		{name: "prod over plain http", set: map[string]string{"APP_ENV": "prod", "SESSION_SECRET": strings.Repeat("x", 32), "BASE_URL": "http://tixo.me"}, wantVar: "BASE_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			for _, k := range tc.unset {
				delete(env, k)
			}
			for k, v := range tc.set {
				env[k] = v
			}
			_, err := LoadFrom(env)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantVar) {
				t.Errorf("error %q does not name %s", err, tc.wantVar)
			}
		})
	}
}

func TestLoadAcceptsValidProd(t *testing.T) {
	env := validEnv()
	env["APP_ENV"] = "prod"
	env["SESSION_SECRET"] = strings.Repeat("s", 32)
	env["BASE_URL"] = "https://tixo.me"
	cfg, err := LoadFrom(env)
	if err != nil {
		t.Fatalf("valid prod env rejected: %v", err)
	}
	if !cfg.IsProd() {
		t.Error("IsProd() = false for APP_ENV=prod")
	}
}
