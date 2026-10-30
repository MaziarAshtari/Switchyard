// Package config reads Switchyard's configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the process configuration.
type Config struct {
	Port          int    // PORT, default 8080
	DataDir       string // DATA_DIR, default ./data
	AdminPassword string // ADMIN_PASSWORD, only used to initialise the password
	EncryptionKey string // ENCRYPTION_KEY, required
}

// MinEncryptionKeyLength is the minimum accepted ENCRYPTION_KEY length.
const MinEncryptionKeyLength = 16

// FromEnv reads the configuration from the process environment.
func FromEnv() (Config, error) { return Load(os.Getenv) }

// Load reads the configuration using getenv.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Port:          8080,
		DataDir:       "./data",
		AdminPassword: getenv("ADMIN_PASSWORD"),
		EncryptionKey: strings.TrimSpace(getenv("ENCRYPTION_KEY")),
	}
	var errs []error
	if v := strings.TrimSpace(getenv("PORT")); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			errs = append(errs, fmt.Errorf("PORT must be a number between 1 and 65535, got %q", v))
		}
		c.Port = p
	}
	if v := strings.TrimSpace(getenv("DATA_DIR")); v != "" {
		c.DataDir = v
	}
	c.DataDir = filepath.Clean(c.DataDir)
	switch {
	case c.EncryptionKey == "":
		errs = append(errs, errors.New("ENCRYPTION_KEY is required (generate one with: openssl rand -hex 32)"))
	case len(c.EncryptionKey) < MinEncryptionKeyLength:
		errs = append(errs, fmt.Errorf("ENCRYPTION_KEY must be at least %d characters", MinEncryptionKeyLength))
	}
	return c, errors.Join(errs...)
}

// DBPath is the SQLite database file.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "switchyard.db") }
