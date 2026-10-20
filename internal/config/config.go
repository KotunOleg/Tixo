package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

const (
	EnvDev  = "dev"
	EnvTest = "test"
	EnvProd = "prod"

	minSecretLen = 32
)

type Config struct {
	AppEnv        string  `env:"APP_ENV" envDefault:"dev"`
	HTTPAddr      string  `env:"HTTP_ADDR" envDefault:":8080"`
	BaseURL       url.URL `env:"BASE_URL" envDefault:"http://localhost:8080"`
	DatabaseURL   string  `env:"DATABASE_URL,required,notEmpty"`
	RedisURL      string  `env:"REDIS_URL,required,notEmpty"`
	AMQPURL       string  `env:"AMQP_URL,required,notEmpty"`
	SessionSecret string  `env:"SESSION_SECRET"`
}

func (c *Config) IsProd() bool { return c.AppEnv == EnvProd }

func Load() (*Config, error) {
	return LoadFrom(environ())
}

func LoadFrom(vars map[string]string) (*Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: vars}); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	switch c.AppEnv {
	case EnvDev, EnvTest, EnvProd:
	default:
		return fmt.Errorf("APP_ENV must be one of dev|test|prod, got %q", c.AppEnv)
	}

	if c.BaseURL.Scheme != "http" && c.BaseURL.Scheme != "https" || c.BaseURL.Host == "" {
		return fmt.Errorf("BASE_URL must be an absolute http(s) URL, got %q", c.BaseURL.String())
	}
	if strings.Trim(c.BaseURL.Path, "/") != "" || c.BaseURL.RawQuery != "" {
		return fmt.Errorf("BASE_URL must not contain a path or query, got %q", c.BaseURL.String())
	}
	c.BaseURL.Path = ""

	if c.IsProd() {
		if c.BaseURL.Scheme != "https" {
			return fmt.Errorf("BASE_URL must use https in prod, got %q", c.BaseURL.String())
		}
		if len(c.SessionSecret) < minSecretLen {
			return fmt.Errorf("SESSION_SECRET must be at least %d bytes in prod", minSecretLen)
		}
		return nil
	}
	if c.SessionSecret == "" {
		c.SessionSecret = randomHex(minSecretLen)
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

func environ() map[string]string {
	vars := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	return vars
}
