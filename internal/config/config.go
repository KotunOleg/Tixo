package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"

	"github.com/caarlos0/env/v11"
)

const (
	EnvDev  = "dev"
	EnvTest = "test"
	EnvProd = "prod"

	minSecretLen = 32
	maxDBConns   = 50
)

type Config struct {
	AppEnv        string  `env:"APP_ENV" envDefault:"dev"`
	HTTPAddr      string  `env:"HTTP_ADDR" envDefault:":8080"`
	BaseURL       url.URL `env:"BASE_URL" envDefault:"http://localhost:8080"`
	DatabaseURL   string  `env:"DATABASE_URL,required,notEmpty"`
	DBMaxConns    int32   `env:"DB_MAX_CONNS" envDefault:"10"`
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
		return nil, fmt.Errorf("config: %w", withVarNames(err))
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

	if c.DBMaxConns < 1 || c.DBMaxConns > maxDBConns {
		return fmt.Errorf("DB_MAX_CONNS must be between 1 and %d, got %d", maxDBConns, c.DBMaxConns)
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

func withVarNames(err error) error {
	var agg env.AggregateError
	if !errors.As(err, &agg) {
		return err
	}
	msgs := make([]string, 0, len(agg.Errors))
	for _, e := range agg.Errors {
		var pe env.ParseError
		if errors.As(e, &pe) {
			msgs = append(msgs, fmt.Sprintf("%s: %v", envVarName(pe.Name), pe.Err))
			continue
		}
		msgs = append(msgs, e.Error())
	}
	return errors.New(strings.Join(msgs, "; "))
}

func envVarName(field string) string {
	f, ok := reflect.TypeFor[Config]().FieldByName(field)
	if !ok {
		return field
	}
	name, _, _ := strings.Cut(f.Tag.Get("env"), ",")
	return name
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
