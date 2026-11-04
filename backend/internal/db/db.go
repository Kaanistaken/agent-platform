// Package db opens the SQLite database and applies migrations.
//
// D82: one write connection (`_txlock=immediate`) and a pool of read-only
// connections. Pragmas are set in the DSN so every pooled connection gets them,
// and they are verified at start-up (D94).
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DB holds the two connection pools. All writes go through W; reads may use R.
type DB struct {
	W *sql.DB // single writer
	R *sql.DB // read-only pool
}

// Open opens (and creates if needed) the database at path, verifies the
// pragmas and applies pending migrations before anything else uses it.
func Open(ctx context.Context, path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	pragmas := []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)"}
	dsn := func(extra ...string) string {
		q := url.Values{}
		for _, p := range pragmas {
			q.Add("_pragma", p)
		}
		for _, e := range extra {
			k, v, _ := strings.Cut(e, "=")
			q.Add(k, v)
		}
		return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	}

	w, err := sql.Open("sqlite", dsn("_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)

	// Migrations run on the writer before the read pool is opened (D94).
	if err := checkPragmas(ctx, w); err != nil {
		w.Close()
		return nil, err
	}
	if err := migrate(ctx, w); err != nil {
		w.Close()
		return nil, err
	}

	pragmas = append(pragmas, "query_only(1)")
	r, err := sql.Open("sqlite", dsn())
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	if err := checkPragmas(ctx, r); err != nil {
		w.Close()
		r.Close()
		return nil, err
	}
	return &DB{W: w, R: r}, nil
}

// Close closes both pools.
func (d *DB) Close() error {
	err1 := d.R.Close()
	err2 := d.W.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func checkPragmas(ctx context.Context, db *sql.DB) error {
	var fk int
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return err
	}
	if fk != 1 || !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("sqlite pragmas not applied (foreign_keys=%d, journal_mode=%s)", fk, mode)
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			name, Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// TimeFormat is how timestamps are stored: UTC, RFC 3339 with milliseconds,
// so that text comparison orders them correctly.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// Now returns the current time in TimeFormat.
func Now() string { return time.Now().UTC().Format(TimeFormat) }

// Format formats t in TimeFormat.
func Format(t time.Time) string { return t.UTC().Format(TimeFormat) }

// Parse parses a stored timestamp.
func Parse(s string) (time.Time, error) { return time.Parse(TimeFormat, s) }
