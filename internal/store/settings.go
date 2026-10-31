package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/secret"
)

// Settings are the user-editable runtime settings.
type Settings struct {
	LogRetentionDays       int `json:"log_retention_days"`
	DefaultCooldownSeconds int `json:"default_cooldown_seconds"`
	// DisabledKeyRecoveryMinutes re-activates keys disabled by 401/402/403
	// after this long (0 = never); providers may override it.
	DisabledKeyRecoveryMinutes int    `json:"disabled_key_recovery_minutes"`
	LogBodies                  bool   `json:"log_bodies"`
	LogBodyMaxBytes            int    `json:"log_body_max_bytes"`
	ProxyTestURL               string `json:"proxy_test_url"`
}

// MaxKeyRecoveryMinutes bounds the disabled-key recovery period (one year).
const MaxKeyRecoveryMinutes = 525600

// DefaultSettings are used for settings that were never saved.
func DefaultSettings() Settings {
	return Settings{
		LogRetentionDays:       30,
		DefaultCooldownSeconds: 60,
		LogBodies:              false,
		LogBodyMaxBytes:        64 << 10,
		ProxyTestURL:           "https://www.gstatic.com/generate_204",
	}
}

const (
	keyPasswordHash    = "admin_password_hash"
	keyEncryptionCheck = "encryption_check"
	encryptionCheck    = "switchyard"
)

// Settings returns the current settings (cached in memory).
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.settings != nil {
		return *s.settings, nil
	}
	set := DefaultSettings()
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return set, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return set, err
		}
		n, _ := strconv.Atoi(v)
		switch k {
		case "log_retention_days":
			set.LogRetentionDays = n
		case "default_cooldown_seconds":
			set.DefaultCooldownSeconds = n
		case "disabled_key_recovery_minutes":
			set.DisabledKeyRecoveryMinutes = n
		case "log_bodies":
			set.LogBodies = v == "true"
		case "log_body_max_bytes":
			set.LogBodyMaxBytes = n
		case "proxy_test_url":
			set.ProxyTestURL = v
		}
	}
	if err := rows.Err(); err != nil {
		return set, err
	}
	s.settings = &set
	return set, nil
}

// SaveSettings validates and stores the settings.
func (s *Store) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	errs := FieldErrors{}
	in.ProxyTestURL = strings.TrimSpace(in.ProxyTestURL)
	if in.LogRetentionDays < 1 || in.LogRetentionDays > 3650 {
		errs["log_retention_days"] = "Must be between 1 and 3650 days"
	}
	if in.DefaultCooldownSeconds < 1 || in.DefaultCooldownSeconds > 86400 {
		errs["default_cooldown_seconds"] = "Must be between 1 and 86400 seconds"
	}
	if msg := checkKeyRecovery(in.DisabledKeyRecoveryMinutes); msg != "" {
		errs["disabled_key_recovery_minutes"] = msg
	}
	if in.LogBodyMaxBytes < 1024 || in.LogBodyMaxBytes > 10<<20 {
		errs["log_body_max_bytes"] = "Must be between 1 KB and 10 MB"
	}
	if u, err := url.Parse(in.ProxyTestURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs["proxy_test_url"] = "Enter a full URL starting with http:// or https://"
	}
	if len(errs) > 0 {
		return Settings{}, errs
	}
	values := map[string]string{
		"log_retention_days":            strconv.Itoa(in.LogRetentionDays),
		"default_cooldown_seconds":      strconv.Itoa(in.DefaultCooldownSeconds),
		"disabled_key_recovery_minutes": strconv.Itoa(in.DisabledKeyRecoveryMinutes),
		"log_bodies":                    strconv.FormatBool(in.LogBodies),
		"log_body_max_bytes":            strconv.Itoa(in.LogBodyMaxBytes),
		"proxy_test_url":                in.ProxyTestURL,
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for k, v := range values {
			if err := setSetting(ctx, tx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	s.settingsMu.Lock()
	s.settings = &in
	s.settingsMu.Unlock()
	return in, nil
}

func checkKeyRecovery(minutes int) string {
	if minutes < 0 || minutes > MaxKeyRecoveryMinutes {
		return "Must be between 0 (never) and 525600 minutes (one year)"
	}
	return ""
}

func setSetting(ctx context.Context, db execer, key, value string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) getSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// PasswordHash returns the admin password hash ("" when not set yet).
func (s *Store) PasswordHash(ctx context.Context) (string, error) {
	v, _, err := s.getSetting(ctx, keyPasswordHash)
	return v, err
}

// SetPassword hashes and stores the admin password.
func (s *Store) SetPassword(ctx context.Context, password string) error {
	hash, err := secret.HashPassword(password)
	if err != nil {
		return err
	}
	return setSetting(ctx, s.db, keyPasswordHash, hash)
}

// CheckEncryptionKey verifies that the configured ENCRYPTION_KEY matches the
// one used for existing data, recording a check value on first start.
func (s *Store) CheckEncryptionKey(ctx context.Context) error {
	v, ok, err := s.getSetting(ctx, keyEncryptionCheck)
	if err != nil {
		return err
	}
	if !ok {
		enc, err := s.box.Encrypt(encryptionCheck)
		if err != nil {
			return err
		}
		return setSetting(ctx, s.db, keyEncryptionCheck, enc)
	}
	plain, err := s.box.Decrypt(v)
	if err != nil || plain != encryptionCheck {
		return errors.New("ENCRYPTION_KEY does not match the key this database was created with")
	}
	return nil
}

// CreateSession stores a session token hash.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, created_at, expires_at) VALUES (?, ?, ?)`,
		tokenHash, ms(s.now()), ms(expires))
	return err
}

// SessionValid reports whether a session exists and has not expired.
func (s *Store) SessionValid(ctx context.Context, tokenHash string) (bool, error) {
	return s.exists(ctx, `SELECT COUNT(*) FROM sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash, ms(s.now()))
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteOtherSessions removes all sessions except keep ("" removes all).
func (s *Store) DeleteOtherSessions(ctx context.Context, keep string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash != ?`, keep)
	return err
}
