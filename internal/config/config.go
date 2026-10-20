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

	// PushgatewayURL receives ingest metrics at the end of a run; empty means
	// do not push.
	PushgatewayURL string `env:"PUSHGATEWAY_URL" validate:"omitempty,url"`

	IngestUserAgent string `env:"INGEST_USER_AGENT" envDefault:"calbot/1.0" validate:"required"`
	SilpoBranchID   string `env:"SILPO_BRANCH_ID"   envDefault:"00000000-0000-0000-0000-000000000000"`
	// IngestRejectCap bounds the review-queue rows one run stores per distinct
	// rejection reason; the rest are only counted.
	IngestRejectCap int    `env:"INGEST_REJECT_CAP" envDefault:"50"          validate:"min=0"`
	OFFDumpURL      string `env:"OFF_DUMP_URL"      envDefault:"https://static.openfoodfacts.org/data/en.openfoodfacts.org.products.csv.gz" validate:"required,url"`
	McDonaldsPDFURL string `env:"MCDONALDS_PDF_URL" envDefault:"https://ukraine.mcdonalds.ua/static/restaurants/consist.pdf" validate:"required,url"`

	// ZakazStoreIDs is one store per chain: Novus, Auchan, Таврія В, Космос,
	// Megamarket, ЕКО маркет. Each chain's kitchen publishes nutrition for its
	// in-house food, and stores of one chain share their chain-coded items.
	ZakazStoreIDs []string `env:"ZAKAZ_STORE_IDS" envDefault:"482010105,48246401,482211449,48225531,48267601,482800030" envSeparator:"," validate:"min=1,dive,required"`
	// ForaFilialIDs is an ordered fallback list of big Kyiv stores; each stocks
	// a subset of the range, so the adapter unions them.
	ForaFilialIDs []int    `env:"FORA_FILIAL_IDS" envDefault:"3433,3410,3805,1851,3356" envSeparator:"," validate:"min=1,dive,gt=0"`
	USDAAPIKey    string   `env:"USDA_API_KEY"    envDefault:"DEMO_KEY" validate:"required"`
	USDAQueries   []string `env:"USDA_QUERIES"    envDefault:"chicken breast,beef,pork,egg,milk,cheese,butter,rice,buckwheat,oats,pasta,bread,potato,tomato,cucumber,carrot,cabbage,onion,apple,banana,orange,salmon,tuna,lentils,sunflower oil,olive oil,sugar,honey" envSeparator:"," validate:"min=1,dive,required"`
	ProeBaseURL   string   `env:"PROE_BASE_URL"   envDefault:"https://proe.info" validate:"required,url"`
}

// IsProduction gates cookie flags and error verbosity.
func (c Config) IsProduction() bool { return c.Env == "production" }

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
