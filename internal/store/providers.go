package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/routing"
)

// Provider types and key strategies.
var (
	ProviderTypes = []string{"openai", "anthropic", "gemini"}
	KeyStrategies = []string{routing.KeyRoundRobin, routing.KeyRandom, routing.KeyLRU}
)

// Provider is an upstream AI provider.
type Provider struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	BaseURL        string            `json:"base_url"`
	Headers        map[string]string `json:"headers"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Enabled        bool              `json:"enabled"`
	ProxyID        *int64            `json:"proxy_id"`
	ProxyName      string            `json:"proxy_name"`
	KeyStrategy    string            `json:"key_strategy"`
	// DisabledKeyRecoveryMinutes overrides the global setting; nil = use it.
	DisabledKeyRecoveryMinutes *int      `json:"disabled_key_recovery_minutes"`
	CreatedAt                  time.Time `json:"created_at"`
	Keys                       KeyCounts `json:"keys"`
	ModelCount                 int       `json:"model_count"`
}

// KeyRecovery returns how long keys of p disabled by an auth error stay
// disabled before they become active again; 0 means until reset.
func (p Provider) KeyRecovery(set Settings) time.Duration {
	minutes := set.DisabledKeyRecoveryMinutes
	if p.DisabledKeyRecoveryMinutes != nil {
		minutes = *p.DisabledKeyRecoveryMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// ProviderInput is the editable part of a provider.
type ProviderInput struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	BaseURL        string            `json:"base_url"`
	Headers        map[string]string `json:"headers"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Enabled        bool              `json:"enabled"`
	ProxyID        *int64            `json:"proxy_id"`
	KeyStrategy    string            `json:"key_strategy"`
	// DisabledKeyRecoveryMinutes: nil = use the global setting.
	DisabledKeyRecoveryMinutes *int `json:"disabled_key_recovery_minutes"`
}

func (s *Store) normalizeProvider(ctx context.Context, in *ProviderInput) error {
	errs := FieldErrors{}
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.Name == "" {
		errs["name"] = "Name is required"
	}
	if !contains(ProviderTypes, in.Type) {
		errs["type"] = "Choose openai, anthropic or gemini"
	}
	if u, err := url.Parse(in.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs["base_url"] = "Enter a full URL starting with http:// or https://"
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = 600
	}
	if in.TimeoutSeconds < 1 || in.TimeoutSeconds > 3600 {
		errs["timeout_seconds"] = "Must be between 1 and 3600 seconds"
	}
	if in.KeyStrategy == "" {
		in.KeyStrategy = routing.KeyRoundRobin
	}
	if !contains(KeyStrategies, in.KeyStrategy) {
		errs["key_strategy"] = "Choose round_robin, random or least_recently_used"
	}
	if in.DisabledKeyRecoveryMinutes != nil {
		if msg := checkKeyRecovery(*in.DisabledKeyRecoveryMinutes); msg != "" {
			errs["disabled_key_recovery_minutes"] = msg
		}
	}
	headers := map[string]string{}
	for k, v := range in.Headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !validHeaderName(k) {
			errs["headers"] = fmt.Sprintf("Invalid header name %q", k)
			continue
		}
		headers[http.CanonicalHeaderKey(k)] = strings.TrimSpace(v)
	}
	in.Headers = headers
	if in.ProxyID != nil {
		ok, err := s.exists(ctx, `SELECT COUNT(*) FROM proxies WHERE id = ?`, *in.ProxyID)
		if err != nil {
			return err
		}
		if !ok {
			errs["proxy_id"] = "Proxy not found"
		}
	}
	return errs.orNil()
}

func validHeaderName(k string) bool {
	for _, r := range k {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

const providerSelect = `SELECT p.id, p.name, p.type, p.base_url, p.headers, p.timeout_seconds, p.enabled,
	p.proxy_id, COALESCE(px.name, ''), p.key_strategy, p.disabled_key_recovery_minutes, p.created_at,
	(SELECT COUNT(DISTINCT t.model_id) FROM model_targets t WHERE t.provider_id = p.id)
	FROM providers p LEFT JOIN proxies px ON px.id = p.proxy_id`

func scanProvider(row interface{ Scan(...any) error }) (Provider, error) {
	var (
		p        Provider
		headers  string
		enabled  int
		proxyID  sql.NullInt64
		recovery sql.NullInt64
		created  int64
	)
	err := row.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &headers, &p.TimeoutSeconds, &enabled,
		&proxyID, &p.ProxyName, &p.KeyStrategy, &recovery, &created, &p.ModelCount)
	if err != nil {
		return p, err
	}
	p.Enabled = enabled == 1
	if proxyID.Valid {
		p.ProxyID = &proxyID.Int64
	}
	if recovery.Valid {
		minutes := int(recovery.Int64)
		p.DisabledKeyRecoveryMinutes = &minutes
	}
	p.CreatedAt = fromMS(created)
	p.Headers = map[string]string{}
	_ = json.Unmarshal([]byte(headers), &p.Headers)
	return p, nil
}

// ListProviders returns all providers sorted by name, with key counts.
func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, providerSelect+` ORDER BY p.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	counts, err := s.keyCounts(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Keys = counts[out[i].ID]
	}
	return out, nil
}

// GetProvider returns one provider with key counts.
func (s *Store) GetProvider(ctx context.Context, id int64) (Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, providerSelect+` WHERE p.id = ?`, id))
	if err != nil {
		return p, notFound(err)
	}
	counts, err := s.keyCounts(ctx)
	if err != nil {
		return p, err
	}
	p.Keys = counts[id]
	return p, nil
}

// LookupProvider returns one provider without key counts (cheap; used per request).
func (s *Store) LookupProvider(ctx context.Context, id int64) (Provider, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, providerSelect+` WHERE p.id = ?`, id))
	return p, notFound(err)
}

// CreateProvider validates and inserts a provider.
func (s *Store) CreateProvider(ctx context.Context, in ProviderInput) (Provider, error) {
	if err := s.normalizeProvider(ctx, &in); err != nil {
		return Provider{}, err
	}
	headers, _ := json.Marshal(in.Headers)
	res, err := s.db.ExecContext(ctx, `INSERT INTO providers
		(name, type, base_url, headers, timeout_seconds, enabled, proxy_id, key_strategy, disabled_key_recovery_minutes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, in.Type, in.BaseURL, string(headers), in.TimeoutSeconds, boolInt(in.Enabled), in.ProxyID,
		in.KeyStrategy, in.DisabledKeyRecoveryMinutes, ms(s.now()))
	if isUnique(err) {
		return Provider{}, FieldErrors{"name": "A provider with this name already exists"}
	}
	if err != nil {
		return Provider{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetProvider(ctx, id)
}

// UpdateProvider validates and updates a provider.
func (s *Store) UpdateProvider(ctx context.Context, id int64, in ProviderInput) (Provider, error) {
	if err := s.normalizeProvider(ctx, &in); err != nil {
		return Provider{}, err
	}
	headers, _ := json.Marshal(in.Headers)
	res, err := s.db.ExecContext(ctx, `UPDATE providers SET name = ?, type = ?, base_url = ?, headers = ?,
		timeout_seconds = ?, enabled = ?, proxy_id = ?, key_strategy = ?, disabled_key_recovery_minutes = ? WHERE id = ?`,
		in.Name, in.Type, in.BaseURL, string(headers), in.TimeoutSeconds, boolInt(in.Enabled), in.ProxyID,
		in.KeyStrategy, in.DisabledKeyRecoveryMinutes, id)
	if isUnique(err) {
		return Provider{}, FieldErrors{"name": "A provider with this name already exists"}
	}
	if err != nil {
		return Provider{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Provider{}, ErrNotFound
	}
	return s.GetProvider(ctx, id)
}

// DeleteProvider deletes a provider with its keys and model targets.
func (s *Store) DeleteProvider(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
