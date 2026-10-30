package db

import (
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("migrations not recorded: n=%d err=%v", n, err)
	}
	var fk int
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys not enabled: %d %v", fk, err)
	}
	d.Close()

	d, err = Open(path) // second open must not re-apply migrations
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var m int
	d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&m)
	if m != n {
		t.Fatalf("migrations re-applied: %d != %d", m, n)
	}
}
