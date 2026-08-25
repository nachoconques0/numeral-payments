// Package config loads the service settings from the environment.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Defaults applied when the optional variables are not set.
const (
	DefaultAddr         = ":8080"
	DefaultPollInterval = 2 * time.Second
	DefaultUsername     = "CALCAGNO"
	DefaultPassword     = "xxxx"
	DefaultBankAdapter  = "xml"
)

// Auth holds the HTTP basic auth credentials.
type Auth struct {
	Username string
	Password string
}

// Config is the full service configuration.
type Config struct {
	Addr                 string
	BankFolder           string
	BankAdapter          string
	SQLiteDBFileLocation string
	PollInterval         time.Duration
	Auth                 Auth
}

// Load reads the configuration from the environment. BANK_FOLDER and
// SQLITE_DB_FILE_LOCATION are required; everything else has a working default.
func Load() (Config, error) {
	cfg := Config{
		Addr:                 lookup("ADDR", DefaultAddr),
		BankFolder:           os.Getenv("BANK_FOLDER"),
		BankAdapter:          strings.ToLower(lookup("BANK_ADAPTER", DefaultBankAdapter)),
		SQLiteDBFileLocation: os.Getenv("SQLITE_DB_FILE_LOCATION"),
		PollInterval:         DefaultPollInterval,
		Auth: Auth{
			Username: lookup("AUTH_USERNAME", DefaultUsername),
			Password: lookup("AUTH_PASSWORD", DefaultPassword),
		},
	}

	if raw := os.Getenv("POLL_INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("POLL_INTERVAL %q is not a duration such as 2s: %w", raw, err)
		}
		if interval <= 0 {
			return Config{}, fmt.Errorf("POLL_INTERVAL must be greater than zero, got %s", raw)
		}
		cfg.PollInterval = interval
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) validate() error {
	var missing []string
	if c.BankFolder == "" {
		missing = append(missing, "BANK_FOLDER")
	}
	if c.SQLiteDBFileLocation == "" {
		missing = append(missing, "SQLITE_DB_FILE_LOCATION")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func lookup(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
