// Package store owns the application's SQLite connection and schema migrations.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	maxOpenConnections = 4
	maxIdleConnections = 2
	connectionLifetime = 5 * time.Minute
)

// Store is the sole owner of the application's SQLite connection pool.
type Store struct {
	db *sql.DB
}

// Open opens path with the SQLite safety and durability policy used by the
// application. It verifies the connection before returning it to the caller.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := sqliteDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConnections)
	db.SetMaxIdleConns(maxIdleConnections)
	db.SetConnMaxLifetime(connectionLifetime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping SQLite database: %w", err)
	}

	return &Store{db: db}, nil
}

// DB returns the connection pool owned by the store.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the store's database connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

func sqliteDSN(path string) string {
	escapedPath := strings.NewReplacer(
		"%", "%25",
		"?", "%3F",
		"#", "%23",
	).Replace(filepath.ToSlash(path))
	u := &url.URL{Scheme: "file", Opaque: escapedPath}
	query := u.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "trusted_schema(0)")
	query.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = query.Encode()
	return u.String()
}
