// Package config loads every setting from the environment into one typed
// struct, validated at startup. Nothing reads os.Getenv anywhere else.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
)

type Config struct {
	Env      string `env:"ENV"       envDefault:"development" validate:"required,oneof=development production"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"        validate:"required,oneof=debug info warn error"`
	Timezone string `env:"TZ"        envDefault:"Europe/Kyiv" validate:"required"`

	HTTPAddr  string `env:"HTTP_ADDR"  envDefault:":8080" validate:"required"`
	PublicURL string `env:"PUBLIC_URL" validate:"required,url"`

	DatabaseURL string `env:"DATABASE_URL" validate:"required,url"`
	RedisURL    string `env:"REDIS_URL"    validate:"required,url"`

	BotToken string `env:"BOT_TOKEN"        validate:"required"`
	// ServiceToken authenticates the bot process against the API. The Mini App
	// authenticates with Telegram initData instead and never sees this.
	ServiceToken   string  `env:"SERVICE_TOKEN"    validate:"required,min=32"`
	AllowedUsers   []int64 `env:"ALLOWED_USERS"    envSeparator:","`
	TelegramAPIURL string  `env:"TELEGRAM_API_URL"`

	OpenAIKey   string `env:"OPENAI_API_KEY" validate:"required"`
	OpenAIModel string `env:"OPENAI_MODEL"   envDefault:"gpt-4.1-mini" validate:"required"`

	IngestUserAgent string `env:"INGEST_USER_AGENT" envDefault:"calbot/1.0" validate:"required"`
	SilpoBranchID   string `env:"SILPO_BRANCH_ID"   envDefault:"00000000-0000-0000-0000-000000000000"`
	OFFDumpURL      string `env:"OFF_DUMP_URL"      envDefault:"https://static.openfoodfacts.org/data/openfoodfacts-products.jsonl.gz" validate:"required,url"`

	ZakazStoreIDs []string `env:"ZAKAZ_STORE_IDS" envDefault:"482010105" envSeparator:"," validate:"min=1,dive,required"`
	USDAAPIKey    string   `env:"USDA_API_KEY"    envDefault:"DEMO_KEY" validate:"required"`
	USDAQueries   []string `env:"USDA_QUERIES"    envDefault:"chicken breast,beef,pork,egg,milk,cheese,butter,rice,buckwheat,oats,pasta,bread,potato,tomato,cucumber,carrot,cabbage,onion,apple,banana,orange,salmon,tuna,lentils,sunflower oil,olive oil,sugar,honey" envSeparator:"," validate:"min=1,dive,required"`
	ProeBaseURL   string   `env:"PROE_BASE_URL"   envDefault:"https://proe.info" validate:"required,url"`
}

// IsProduction gates cookie flags and error verbosity.
func (c Config) IsProduction() bool { return c.Env == "production" }

// PublicBot reports whether the bot answers anyone. An empty allow list means
// public, which is worth saying out loud at startup rather than discovering.
func (c Config) PublicBot() bool { return len(c.AllowedUsers) == 0 }

// Load reads .env if present, falls back to the OS environment, and validates.
// It returns an error rather than exiting so main owns the exit.
func Load() (Config, error) {
	_ = godotenv.Load()

	var cfg Config
	err := env.Parse(&cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}

	err = validator.New().Struct(cfg)
	if err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}
