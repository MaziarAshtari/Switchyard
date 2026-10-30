// Package store contains all SQL access. Its types double as the JSON shapes
// of the admin API; secrets never leave the package in clear text except
// through the explicit *Secret methods used by the gateway.
package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/secret"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// FieldErrors maps input field names to validation messages.
type FieldErrors map[string]string

func (f FieldErrors) Error() string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+f[k])
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func (f FieldErrors) orNil() error {
	if len(f) == 0 {
		return nil
	}
	return f
}

// Store wraps the database.
type Store struct {
	db  *sql.DB
	box *secret.Box
	now func() time.Time

	settingsMu sync.Mutex
	settings   *Settings
}

// New returns a Store using db and box for secrets.
func New(db *sql.DB, box *secret.Box) *Store {
	return &Store{db: db, box: box, now: time.Now}
}

// SetClock replaces the clock (tests).
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// DB exposes the underlying database (tests and maintenance).
func (s *Store) DB() *sql.DB { return s.db }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func optTime(v int64) *time.Time {
	if v == 0 {
		return nil
	}
	t := fromMS(v)
	return &t
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// tx runs fn in a transaction.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) exists(ctx context.Context, query string, args ...any) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
