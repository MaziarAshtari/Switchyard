package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MaziarAshtari/Switchyard/internal/routing"
	"github.com/MaziarAshtari/Switchyard/internal/secret"
)

// Proxy modes of an API key.
const (
	ProxyInherit  = "inherit"
	ProxyDirect   = "direct"
	ProxySpecific = "specific"
)

// ProxyModes lists the valid proxy modes.
var ProxyModes = []string{ProxyInherit, ProxyDirect, ProxySpecific}

// APIKey is an upstream API key (the secret itself is never included).
type APIKey struct {
	ID             int64      `json:"id"`
	ProviderID     int64      `json:"provider_id"`
	ProviderName   string     `json:"provider_name"`
	Label          string     `json:"label"`
	SecretHint     string     `json:"secret_hint"`
	Enabled        bool       `json:"enabled"`
	ProxyMode      string     `json:"proxy_mode"`
	ProxyID        *int64     `json:"proxy_id"`
	EffectiveProxy *ProxyRef  `json:"effective_proxy"` // nil means direct
	Status         string     `json:"status"`          // effective: expired cooldowns read as active
	CooldownUntil  *time.Time `json:"cooldown_until"`
	LastError      string     `json:"last_error"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	SuccessCount   int64      `json:"success_count"`
	FailureCount   int64      `json:"failure_count"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ProxyRef identifies the proxy a key actually uses.
type ProxyRef struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// ProxyOK reports whether the key's proxy setting allows using it: direct,
// or an existing enabled proxy.
func (k APIKey) ProxyOK() bool {
	if k.EffectiveProxy == nil {
		return k.ProxyMode != ProxySpecific
	}
	return k.EffectiveProxy.Enabled
}

// RoutingKey converts k to the routing package's view.
func (k APIKey) RoutingKey() routing.Key {
	rk := routing.Key{ID: k.ID, Enabled: k.Enabled, Status: k.Status, ProxyOK: k.ProxyOK()}
	if k.CooldownUntil != nil {
		rk.CooldownUntil = *k.CooldownUntil
	}
	if k.LastUsedAt != nil {
		rk.LastUsedAt = *k.LastUsedAt
	}
	return rk
}

// KeyCounts summarises key health. Off counts keys switched off by the user;
// they are not counted in the other buckets.
type KeyCounts struct {
	Total       int `json:"total"`
	Active      int `json:"active"`
	CoolingDown int `json:"cooling_down"`
	Disabled    int `json:"disabled"`
	Off         int `json:"off"`
}

func (c *KeyCounts) add(enabled bool, status string) {
	c.Total++
	switch {
	case !enabled:
		c.Off++
	case status == routing.StatusCoolingDown:
		c.CoolingDown++
	case status == routing.StatusDisabled:
		c.Disabled++
	default:
		c.Active++
	}
}

// KeyInput creates a key.
type KeyInput struct {
	Label     string `json:"label"`
	Secret    string `json:"secret"`
	ProxyMode string `json:"proxy_mode"`
	ProxyID   *int64 `json:"proxy_id"`
}

// KeyUpdate edits a key.
type KeyUpdate struct {
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	ProxyMode string `json:"proxy_mode"`
	ProxyID   *int64 `json:"proxy_id"`
}

// BulkResult reports a bulk key import.
type BulkResult struct {
	Added   int         `json:"added"`
	Skipped int         `json:"skipped"`
	Errors  []LineError `json:"errors"`
}

// LineError is a problem with one input line (1-based).
type LineError struct {
	Line  int    `json:"line"`
	Error string `json:"error"`
}

const keySelect = `SELECT k.id, k.provider_id, p.name, k.label, k.secret_hint, k.enabled, k.proxy_mode, k.proxy_id,
	k.status, k.cooldown_until, k.last_error, k.last_used_at, k.success_count, k.failure_count, k.created_at,
	px.id, px.name, px.enabled
	FROM api_keys k
	JOIN providers p ON p.id = k.provider_id
	LEFT JOIN proxies px ON px.id = CASE k.proxy_mode
		WHEN 'specific' THEN k.proxy_id WHEN 'inherit' THEN p.proxy_id END`

func (s *Store) scanKey(row interface{ Scan(...any) error }) (APIKey, error) {
	var (
		k                          APIKey
		enabled                    int
		proxyID, pxID, pxEnabled   sql.NullInt64
		pxName                     sql.NullString
		cooldown, lastUsed, create int64
	)
	err := row.Scan(&k.ID, &k.ProviderID, &k.ProviderName, &k.Label, &k.SecretHint, &enabled, &k.ProxyMode, &proxyID,
		&k.Status, &cooldown, &k.LastError, &lastUsed, &k.SuccessCount, &k.FailureCount, &create,
		&pxID, &pxName, &pxEnabled)
	if err != nil {
		return k, err
	}
	k.Enabled = enabled == 1
	if proxyID.Valid {
		k.ProxyID = &proxyID.Int64
	}
	if pxID.Valid {
		k.EffectiveProxy = &ProxyRef{ID: pxID.Int64, Name: pxName.String, Enabled: pxEnabled.Int64 == 1}
	}
	if k.Status == routing.StatusCoolingDown {
		if cooldown > ms(s.now()) {
			k.CooldownUntil = optTime(cooldown)
		} else {
			k.Status = routing.StatusActive
		}
	}
	k.LastUsedAt = optTime(lastUsed)
	k.CreatedAt = fromMS(create)
	return k, nil
}

func (s *Store) queryKeys(ctx context.Context, where string, args ...any) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, keySelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := s.scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListKeys returns the keys of a provider, or of all providers when providerID is 0.
func (s *Store) ListKeys(ctx context.Context, providerID int64) ([]APIKey, error) {
	if providerID == 0 {
		return s.queryKeys(ctx, `ORDER BY p.name COLLATE NOCASE, k.id`)
	}
	return s.queryKeys(ctx, `WHERE k.provider_id = ? ORDER BY k.id`, providerID)
}

// GetKey returns one key.
func (s *Store) GetKey(ctx context.Context, id int64) (APIKey, error) {
	keys, err := s.queryKeys(ctx, `WHERE k.id = ?`, id)
	if err != nil {
		return APIKey{}, err
	}
	if len(keys) == 0 {
		return APIKey{}, ErrNotFound
	}
	return keys[0], nil
}

func (s *Store) keyCounts(ctx context.Context) (map[int64]KeyCounts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider_id, enabled, status, cooldown_until FROM api_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := ms(s.now())
	out := map[int64]KeyCounts{}
	for rows.Next() {
		var (
			pid, cooldown int64
			enabled       int
			status        string
		)
		if err := rows.Scan(&pid, &enabled, &status, &cooldown); err != nil {
			return nil, err
		}
		if status == routing.StatusCoolingDown && cooldown <= now {
			status = routing.StatusActive
		}
		c := out[pid]
		c.add(enabled == 1, status)
		out[pid] = c
	}
	return out, rows.Err()
}

func (s *Store) checkProxyMode(ctx context.Context, mode *string, proxyID **int64, errs FieldErrors) error {
	if *mode == "" {
		*mode = ProxyInherit
	}
	if !contains(ProxyModes, *mode) {
		errs["proxy_mode"] = "Choose inherit, direct or specific"
		return nil
	}
	if *mode != ProxySpecific {
		*proxyID = nil
		return nil
	}
	if *proxyID == nil {
		errs["proxy_id"] = "Choose a proxy"
		return nil
	}
	ok, err := s.exists(ctx, `SELECT COUNT(*) FROM proxies WHERE id = ?`, **proxyID)
	if err != nil {
		return err
	}
	if !ok {
		errs["proxy_id"] = "Proxy not found"
	}
	return nil
}

func (s *Store) providerExists(ctx context.Context, id int64) error {
	ok, err := s.exists(ctx, `SELECT COUNT(*) FROM providers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// CreateKey adds one key to a provider.
func (s *Store) CreateKey(ctx context.Context, providerID int64, in KeyInput) (APIKey, error) {
	if err := s.providerExists(ctx, providerID); err != nil {
		return APIKey{}, err
	}
	errs := FieldErrors{}
	in.Label = strings.TrimSpace(in.Label)
	in.Secret = strings.TrimSpace(in.Secret)
	if in.Secret == "" {
		errs["secret"] = "API key is required"
	} else if strings.ContainsAny(in.Secret, " \t\r\n") {
		errs["secret"] = "API key must not contain spaces"
	}
	if err := s.checkProxyMode(ctx, &in.ProxyMode, &in.ProxyID, errs); err != nil {
		return APIKey{}, err
	}
	if len(errs) > 0 {
		return APIKey{}, errs
	}
	id, err := s.insertKey(ctx, s.db, providerID, in)
	if isUnique(err) {
		return APIKey{}, FieldErrors{"secret": "This key already exists for this provider"}
	}
	if err != nil {
		return APIKey{}, err
	}
	return s.GetKey(ctx, id)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) insertKey(ctx context.Context, db execer, providerID int64, in KeyInput) (int64, error) {
	enc, err := s.box.Encrypt(in.Secret)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, `INSERT INTO api_keys
		(provider_id, label, secret_enc, secret_hash, secret_hint, proxy_mode, proxy_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		providerID, in.Label, enc, s.box.Fingerprint(in.Secret), secret.Mask(in.Secret), in.ProxyMode, in.ProxyID, ms(s.now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BulkAddKeys adds one key per line ("key" or "key|label"), skipping duplicates.
func (s *Store) BulkAddKeys(ctx context.Context, providerID int64, text string) (BulkResult, error) {
	if err := s.providerExists(ctx, providerID); err != nil {
		return BulkResult{}, err
	}
	res := BulkResult{Errors: []LineError{}}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for i, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			key, label, _ := strings.Cut(line, "|")
			key, label = strings.TrimSpace(key), strings.TrimSpace(label)
			switch {
			case key == "":
				res.Errors = append(res.Errors, LineError{i + 1, "empty key"})
				continue
			case strings.ContainsAny(key, " \t"):
				res.Errors = append(res.Errors, LineError{i + 1, "key contains spaces"})
				continue
			}
			_, err := s.insertKey(ctx, tx, providerID, KeyInput{Label: label, Secret: key, ProxyMode: ProxyInherit})
			if isUnique(err) {
				res.Skipped++
				continue
			}
			if err != nil {
				return err
			}
			res.Added++
		}
		return nil
	})
	return res, err
}

// UpdateKey edits label, enabled flag and proxy settings.
func (s *Store) UpdateKey(ctx context.Context, id int64, in KeyUpdate) (APIKey, error) {
	errs := FieldErrors{}
	in.Label = strings.TrimSpace(in.Label)
	if err := s.checkProxyMode(ctx, &in.ProxyMode, &in.ProxyID, errs); err != nil {
		return APIKey{}, err
	}
	if len(errs) > 0 {
		return APIKey{}, errs
	}
	res, err := s.db.ExecContext(ctx, `UPDATE api_keys SET label = ?, enabled = ?, proxy_mode = ?, proxy_id = ? WHERE id = ?`,
		in.Label, boolInt(in.Enabled), in.ProxyMode, in.ProxyID, id)
	if err != nil {
		return APIKey{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return APIKey{}, ErrNotFound
	}
	return s.GetKey(ctx, id)
}

// DeleteKey deletes a key.
func (s *Store) DeleteKey(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetKey re-activates a key: clears a cooldown or an automatic disable.
func (s *Store) ResetKey(ctx context.Context, id int64) (APIKey, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE api_keys SET status = 'active', cooldown_until = 0 WHERE id = ?`, id)
	if err != nil {
		return APIKey{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return APIKey{}, ErrNotFound
	}
	return s.GetKey(ctx, id)
}

// Batch actions on keys.
var KeyBatchActions = []string{"enable", "disable", "reset", "delete"}

// BatchKeys applies action to the given keys and returns how many changed.
func (s *Store) BatchKeys(ctx context.Context, ids []int64, action string) (int, error) {
	var query string
	switch action {
	case "enable":
		query = `UPDATE api_keys SET enabled = 1 WHERE id = ?`
	case "disable":
		query = `UPDATE api_keys SET enabled = 0 WHERE id = ?`
	case "reset":
		query = `UPDATE api_keys SET status = 'active', cooldown_until = 0 WHERE id = ?`
	case "delete":
		query = `DELETE FROM api_keys WHERE id = ?`
	default:
		return 0, FieldErrors{"action": "Unknown action"}
	}
	n := 0
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			res, err := tx.ExecContext(ctx, query, id)
			if err != nil {
				return err
			}
			c, _ := res.RowsAffected()
			n += int(c)
		}
		return nil
	})
	return n, err
}

// AssignProxies sets proxy_mode=specific with the given proxy for each key.
func (s *Store) AssignProxies(ctx context.Context, assignment map[int64]int64) (int, error) {
	n := 0
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for keyID, proxyID := range assignment {
			res, err := tx.ExecContext(ctx, `UPDATE api_keys SET proxy_mode = 'specific', proxy_id = ? WHERE id = ?`, proxyID, keyID)
			if err != nil {
				return err
			}
			c, _ := res.RowsAffected()
			n += int(c)
		}
		return nil
	})
	return n, err
}

// KeySecret returns the decrypted secret of a key.
func (s *Store) KeySecret(ctx context.Context, id int64) (string, error) {
	var enc string
	if err := s.db.QueryRowContext(ctx, `SELECT secret_enc FROM api_keys WHERE id = ?`, id).Scan(&enc); err != nil {
		return "", notFound(err)
	}
	return s.box.Decrypt(enc)
}

// RecordKeyOutcome persists how an attempt affected a key.
func (s *Store) RecordKeyOutcome(ctx context.Context, id int64, d routing.Decision, errMsg string) error {
	now := ms(s.now())
	errMsg = truncate(errMsg, 500)
	var (
		query string
		args  []any
	)
	switch d.Action {
	case routing.KeySuccess:
		query, args = `UPDATE api_keys SET last_used_at = ?, success_count = success_count + 1 WHERE id = ?`, []any{now, id}
	case routing.KeyFailure:
		query, args = `UPDATE api_keys SET last_used_at = ?, failure_count = failure_count + 1, last_error = ? WHERE id = ?`,
			[]any{now, errMsg, id}
	case routing.KeyCooldown:
		query = `UPDATE api_keys SET last_used_at = ?, failure_count = failure_count + 1, last_error = ?,
			status = CASE WHEN status = 'disabled' THEN status ELSE 'cooling_down' END, cooldown_until = ? WHERE id = ?`
		args = []any{now, errMsg, ms(d.CooldownUntil), id}
	case routing.KeyDisable:
		query = `UPDATE api_keys SET last_used_at = ?, failure_count = failure_count + 1, last_error = ?,
			status = 'disabled', cooldown_until = 0 WHERE id = ?`
		args = []any{now, errMsg, id}
	case routing.KeyUsed:
		query, args = `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, []any{now, id}
	default:
		return fmt.Errorf("unknown key action %d", d.Action)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// truncate shortens s to at most n bytes on a UTF-8 boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
