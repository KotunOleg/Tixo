package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/caarlos0/env/v11"
)

const (
	EnvDev  = "dev"
	EnvTest = "test"
	EnvProd = "prod"

	minSecretLen        = 32
	maxDBConns          = 50
	maxAgentRatePerHour = 1000
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
	StorageDir    string  `env:"STORAGE_DIR" envDefault:"./data/uploads"`

	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`

	GA4MeasurementID string `env:"GA4_MEASUREMENT_ID"`

	HubSpotAccessToken string `env:"HUBSPOT_ACCESS_TOKEN"`
	CRMDryRun          bool   `env:"CRM_DRY_RUN" envDefault:"true"`

	ICSFunctionURL string `env:"ICS_FUNCTION_URL"`

	TelegramBotToken      string `env:"TELEGRAM_BOT_TOKEN"`
	TelegramBotUsername   string `env:"TELEGRAM_BOT_USERNAME"`
	TelegramWebhookSecret string `env:"TELEGRAM_WEBHOOK_SECRET"`
	TelegramDryRun        bool   `env:"TELEGRAM_DRY_RUN" envDefault:"true"`

	AnthropicAPIKey  string `env:"ANTHROPIC_API_KEY"`
	AgentModel       string `env:"AGENT_MODEL" envDefault:"claude-sonnet-5-5"`
	AgentFakeFlag    bool   `env:"AGENT_FAKE" envDefault:"true"`
	AgentRatePerHour int    `env:"AGENT_RATE_PER_HOUR" envDefault:"20"`
}

func (c *Config) IsProd() bool { return c.AppEnv == EnvProd }

func (c *Config) GoogleEnabled() bool { return c.GoogleClientID != "" && c.GoogleClientSecret != "" }

func (c *Config) GA4Enabled() bool { return c.GA4MeasurementID != "" }

func (c *Config) TelegramEnabled() bool { return c.TelegramBotUsername != "" }

func (c *Config) AgentFake() bool { return c.AgentFakeFlag || c.AnthropicAPIKey == "" }

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

	if strings.TrimSpace(c.StorageDir) == "" {
		return errors.New("STORAGE_DIR must not be empty")
	}

	if c.BaseURL.Scheme != "http" && c.BaseURL.Scheme != "https" || c.BaseURL.Host == "" {
		return fmt.Errorf("BASE_URL must be an absolute http(s) URL, got %q", c.BaseURL.String())
	}
	if strings.Trim(c.BaseURL.Path, "/") != "" || c.BaseURL.RawQuery != "" {
		return fmt.Errorf("BASE_URL must not contain a path or query, got %q", c.BaseURL.String())
	}
	c.BaseURL.Path = ""

	switch {
	case c.GoogleClientID != "" && c.GoogleClientSecret == "":
		return errors.New("GOOGLE_CLIENT_SECRET must be set together with GOOGLE_CLIENT_ID")
	case c.GoogleClientID == "" && c.GoogleClientSecret != "":
		return errors.New("GOOGLE_CLIENT_ID must be set together with GOOGLE_CLIENT_SECRET")
	}

	if !c.CRMDryRun && c.HubSpotAccessToken == "" {
		return errors.New("HUBSPOT_ACCESS_TOKEN must be set when CRM_DRY_RUN=false")
	}

	if err := c.validateTelegram(); err != nil {
		return err
	}

	c.AnthropicAPIKey = strings.TrimSpace(c.AnthropicAPIKey)
	c.AgentModel = strings.TrimSpace(c.AgentModel)
	if c.AgentModel == "" {
		return errors.New("AGENT_MODEL must not be empty")
	}
	if c.AgentRatePerHour < 1 || c.AgentRatePerHour > maxAgentRatePerHour {
		return fmt.Errorf("AGENT_RATE_PER_HOUR must be between 1 and %d, got %d", maxAgentRatePerHour, c.AgentRatePerHour)
	}

	if c.IsProd() {
		if c.BaseURL.Scheme != "https" {
			return fmt.Errorf("BASE_URL must use https in prod, got %q", c.BaseURL.String())
		}
		if len(c.SessionSecret) < minSecretLen {
			return fmt.Errorf("SESSION_SECRET must be at least %d bytes in prod", minSecretLen)
		}
		if !c.GoogleEnabled() {
			return errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required in prod: sign-in uses Google")
		}
		return nil
	}
	if c.SessionSecret == "" {
		c.SessionSecret = randomHex(minSecretLen)
	}
	return nil
}

var (
	telegramSecretPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	telegramUsernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
)

func (c *Config) validateTelegram() error {
	c.TelegramBotUsername = strings.TrimPrefix(c.TelegramBotUsername, "@")
	if c.TelegramBotUsername != "" && !telegramUsernamePattern.MatchString(c.TelegramBotUsername) {
		return fmt.Errorf("TELEGRAM_BOT_USERNAME is not a valid bot username, got %q", c.TelegramBotUsername)
	}
	if c.TelegramWebhookSecret != "" && !telegramSecretPattern.MatchString(c.TelegramWebhookSecret) {
		return errors.New("TELEGRAM_WEBHOOK_SECRET must be 1-256 characters of A-Z, a-z, 0-9, _ and -")
	}
	if c.TelegramDryRun {
		return nil
	}
	switch {
	case c.TelegramBotToken == "":
		return errors.New("TELEGRAM_BOT_TOKEN must be set when TELEGRAM_DRY_RUN=false")
	case c.TelegramWebhookSecret == "":
		return errors.New("TELEGRAM_WEBHOOK_SECRET must be set when TELEGRAM_DRY_RUN=false")
	case c.TelegramBotUsername == "":
		return errors.New("TELEGRAM_BOT_USERNAME must be set when TELEGRAM_DRY_RUN=false")
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
