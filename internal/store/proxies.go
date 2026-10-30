package store

import (
	"context"
	"database/sql"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/proxy"
)

// Proxy is an outbound proxy (the password is never included in JSON).
type Proxy struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	Host            string    `json:"host"`
	Port            int       `json:"port"`
	Username        string    `json:"username"`
	HasPassword     bool      `json:"has_password"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UsedByKeys      int       `json:"used_by_keys"`
	UsedByProviders int       `json:"used_by_providers"`
}

// ProxyInput creates or edits a proxy. On update a nil Password keeps the
// stored one; clearing the username clears the password.
type ProxyInput struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	Username string  `json:"username"`
	Password *string `json:"password"`
	Enabled  bool    `json:"enabled"`
}

// ImportLine is the parse result of one bulk-import line.
type ImportLine struct {
	Line     int    `json:"line"`
	Input    string `json:"input"`
	Status   string `json:"status"` // valid | duplicate | invalid
	Error    string `json:"error"`
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
}

// ImportResult reports a bulk proxy import (or its preview).
type ImportResult struct {
	Lines      []ImportLine `json:"lines"`
	Valid      int          `json:"valid"`
	Duplicates int          `json:"duplicates"`
	Invalid    int          `json:"invalid"`
	Created    int          `json:"created"`
}

const proxySelect = `SELECT x.id, x.name, x.type, x.host, x.port, x.username, x.password_enc != '', x.enabled, x.created_at,
	(SELECT COUNT(*) FROM api_keys k WHERE k.proxy_mode = 'specific' AND k.proxy_id = x.id),
	(SELECT COUNT(*) FROM providers p WHERE p.proxy_id = x.id)
	FROM proxies x`

func scanProxy(row interface{ Scan(...any) error }) (Proxy, error) {
	var (
		p                Proxy
		hasPass, enabled int
		created          int64
	)
	err := row.Scan(&p.ID, &p.Name, &p.Type, &p.Host, &p.Port, &p.Username, &hasPass, &enabled, &created,
		&p.UsedByKeys, &p.UsedByProviders)
	p.HasPassword, p.Enabled, p.CreatedAt = hasPass == 1, enabled == 1, fromMS(created)
	return p, err
}

// ListProxies returns all proxies sorted by name.
func (s *Store) ListProxies(ctx context.Context) ([]Proxy, error) {
	rows, err := s.db.QueryContext(ctx, proxySelect+` ORDER BY x.name COLLATE NOCASE, x.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proxy{}
	for rows.Next() {
		p, err := scanProxy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProxy returns one proxy.
func (s *Store) GetProxy(ctx context.Context, id int64) (Proxy, error) {
	p, err := scanProxy(s.db.QueryRowContext(ctx, proxySelect+` WHERE x.id = ?`, id))
	return p, notFound(err)
}

// ProxySpec returns the connection details of a proxy, with its password
// decrypted, and whether it is enabled.
func (s *Store) ProxySpec(ctx context.Context, id int64) (proxy.Spec, bool, error) {
	var (
		sp      proxy.Spec
		enc     string
		enabled int
	)
	err := s.db.QueryRowContext(ctx, `SELECT type, host, port, username, password_enc, enabled FROM proxies WHERE id = ?`, id).
		Scan(&sp.Type, &sp.Host, &sp.Port, &sp.Username, &enc, &enabled)
	if err != nil {
		return sp, false, notFound(err)
	}
	sp.Password, err = s.box.Decrypt(enc)
	return sp, enabled == 1, err
}

func normalizeProxy(in *ProxyInput) FieldErrors {
	errs := FieldErrors{}
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.ToLower(strings.TrimSpace(in.Host))
	in.Username = strings.TrimSpace(in.Username)
	if !proxy.ValidType(in.Type) {
		errs["type"] = "Choose http, https or socks5"
	}
	if in.Host == "" {
		errs["host"] = "Host is required"
	} else if net.ParseIP(in.Host) == nil && strings.ContainsAny(in.Host, " /:@") {
		errs["host"] = "Enter only the host name or IP address"
	}
	if in.Port < 1 || in.Port > 65535 {
		errs["port"] = "Port must be between 1 and 65535"
	}
	if in.Name == "" && in.Host != "" {
		in.Name = in.Host + ":" + strconv.Itoa(in.Port)
	}
	return errs
}

// CreateProxy validates and inserts a proxy.
func (s *Store) CreateProxy(ctx context.Context, in ProxyInput) (Proxy, error) {
	if errs := normalizeProxy(&in); len(errs) > 0 {
		return Proxy{}, errs
	}
	pass := ""
	if in.Password != nil && in.Username != "" {
		pass = *in.Password
	}
	enc, err := s.box.Encrypt(pass)
	if err != nil {
		return Proxy{}, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO proxies (name, type, host, port, username, password_enc, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, in.Name, in.Type, in.Host, in.Port, in.Username, enc, boolInt(in.Enabled), ms(s.now()))
	if isUnique(err) {
		return Proxy{}, FieldErrors{"host": "A proxy with the same type, host, port and username already exists"}
	}
	if err != nil {
		return Proxy{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetProxy(ctx, id)
}

// UpdateProxy validates and updates a proxy.
func (s *Store) UpdateProxy(ctx context.Context, id int64, in ProxyInput) (Proxy, error) {
	if errs := normalizeProxy(&in); len(errs) > 0 {
		return Proxy{}, errs
	}
	query := `UPDATE proxies SET name = ?, type = ?, host = ?, port = ?, username = ?, enabled = ? WHERE id = ?`
	args := []any{in.Name, in.Type, in.Host, in.Port, in.Username, boolInt(in.Enabled), id}
	switch {
	case in.Username == "":
		query = `UPDATE proxies SET name = ?, type = ?, host = ?, port = ?, username = ?, enabled = ?, password_enc = '' WHERE id = ?`
	case in.Password != nil:
		enc, err := s.box.Encrypt(*in.Password)
		if err != nil {
			return Proxy{}, err
		}
		query = `UPDATE proxies SET name = ?, type = ?, host = ?, port = ?, username = ?, enabled = ?, password_enc = ? WHERE id = ?`
		args = []any{in.Name, in.Type, in.Host, in.Port, in.Username, boolInt(in.Enabled), enc, id}
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if isUnique(err) {
		return Proxy{}, FieldErrors{"host": "A proxy with the same type, host, port and username already exists"}
	}
	if err != nil {
		return Proxy{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Proxy{}, ErrNotFound
	}
	return s.GetProxy(ctx, id)
}

// DeleteProxy deletes a proxy. Keys that used it switch to "inherit" and
// providers that used it as default fall back to a direct connection.
func (s *Store) DeleteProxy(ctx context.Context, id int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET proxy_mode = 'inherit', proxy_id = NULL WHERE proxy_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE providers SET proxy_id = NULL WHERE proxy_id = ?`, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM proxies WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ImportProxies parses one proxy per line. With dryRun it only reports what
// would be imported; otherwise valid, non-duplicate lines are inserted.
func (s *Store) ImportProxies(ctx context.Context, text, defaultType string, dryRun bool) (ImportResult, error) {
	if !proxy.ValidType(defaultType) {
		return ImportResult{}, FieldErrors{"default_type": "Choose http, https or socks5"}
	}
	seen := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT type, host, port, username FROM proxies`)
	if err != nil {
		return ImportResult{}, err
	}
	for rows.Next() {
		var sp proxy.Spec
		if err := rows.Scan(&sp.Type, &sp.Host, &sp.Port, &sp.Username); err != nil {
			rows.Close()
			return ImportResult{}, err
		}
		seen[sp.Identity()] = true
	}
	rows.Close()

	res := ImportResult{Lines: []ImportLine{}}
	var valid []proxy.Spec
	for i, raw := range strings.Split(text, "\n") {
		input := strings.TrimSpace(raw)
		if input == "" {
			continue
		}
		line := ImportLine{Line: i + 1, Input: input}
		sp, err := proxy.ParseLine(input, defaultType)
		sp.Host = strings.ToLower(sp.Host)
		switch {
		case err != nil:
			line.Status, line.Error = "invalid", err.Error()
			res.Invalid++
		case seen[sp.Identity()]:
			line.Status, line.Error = "duplicate", "already exists"
			res.Duplicates++
		default:
			line.Status = "valid"
			seen[sp.Identity()] = true
			valid = append(valid, sp)
			res.Valid++
		}
		if err == nil {
			line.Type, line.Host, line.Port, line.Username = sp.Type, sp.Host, sp.Port, sp.Username
		}
		res.Lines = append(res.Lines, line)
	}
	if dryRun || len(valid) == 0 {
		return res, nil
	}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		for _, sp := range valid {
			enc, err := s.box.Encrypt(sp.Password)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO proxies (name, type, host, port, username, password_enc, enabled, created_at)
				VALUES (?, ?, ?, ?, ?, ?, 1, ?)`, sp.Host+":"+strconv.Itoa(sp.Port), sp.Type, sp.Host, sp.Port, sp.Username, enc, ms(s.now())); err != nil {
				return err
			}
			res.Created++
		}
		return nil
	})
	return res, err
}
