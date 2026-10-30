package store

import (
	"context"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/secret"
)

// GatewayKey is a client-facing key (the secret is only returned on creation).
type GatewayKey struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	SecretHint string    `json:"secret_hint"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// NewGatewayKey is returned once, when a key is created.
type NewGatewayKey struct {
	GatewayKey
	Secret string `json:"secret"`
}

// GatewayKeyInput edits a gateway key.
type GatewayKeyInput struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

const gatewayKeySelect = `SELECT id, name, secret_hint, enabled, created_at FROM gateway_keys`

func scanGatewayKey(row interface{ Scan(...any) error }) (GatewayKey, error) {
	var (
		k       GatewayKey
		enabled int
		created int64
	)
	err := row.Scan(&k.ID, &k.Name, &k.SecretHint, &enabled, &created)
	k.Enabled, k.CreatedAt = enabled == 1, fromMS(created)
	return k, err
}

// ListGatewayKeys returns all gateway keys.
func (s *Store) ListGatewayKeys(ctx context.Context) ([]GatewayKey, error) {
	rows, err := s.db.QueryContext(ctx, gatewayKeySelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GatewayKey{}
	for rows.Next() {
		k, err := scanGatewayKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// CreateGatewayKey generates a new key; the secret is only returned here.
func (s *Store) CreateGatewayKey(ctx context.Context, name string) (NewGatewayKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return NewGatewayKey{}, FieldErrors{"name": "Name is required"}
	}
	token := secret.NewToken("sy-", 24)
	res, err := s.db.ExecContext(ctx, `INSERT INTO gateway_keys (name, secret_hash, secret_hint, enabled, created_at)
		VALUES (?, ?, ?, 1, ?)`, name, secret.HashToken(token), secret.Mask(token), ms(s.now()))
	if err != nil {
		return NewGatewayKey{}, err
	}
	id, _ := res.LastInsertId()
	k, err := scanGatewayKey(s.db.QueryRowContext(ctx, gatewayKeySelect+` WHERE id = ?`, id))
	return NewGatewayKey{GatewayKey: k, Secret: token}, err
}

// UpdateGatewayKey renames or enables/disables a key.
func (s *Store) UpdateGatewayKey(ctx context.Context, id int64, in GatewayKeyInput) (GatewayKey, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return GatewayKey{}, FieldErrors{"name": "Name is required"}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE gateway_keys SET name = ?, enabled = ? WHERE id = ?`, in.Name, boolInt(in.Enabled), id)
	if err != nil {
		return GatewayKey{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return GatewayKey{}, ErrNotFound
	}
	k, err := scanGatewayKey(s.db.QueryRowContext(ctx, gatewayKeySelect+` WHERE id = ?`, id))
	return k, notFound(err)
}

// DeleteGatewayKey deletes a key.
func (s *Store) DeleteGatewayKey(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM gateway_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateGatewayKey returns the enabled gateway key matching token.
func (s *Store) AuthenticateGatewayKey(ctx context.Context, token string) (GatewayKey, error) {
	k, err := scanGatewayKey(s.db.QueryRowContext(ctx, gatewayKeySelect+` WHERE secret_hash = ? AND enabled = 1`,
		secret.HashToken(token)))
	return k, notFound(err)
}
