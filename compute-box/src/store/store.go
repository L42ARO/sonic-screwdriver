// Package store keeps the compute-box state in SQLite: users, projects, operations,
// machines, jobs, job steps and model usage.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"sonic-screwdriver/compute-box/src/utils"
)

//go:embed schema.sql
var schema string

// DefaultUser owns everything until authentication exists.
const DefaultUser = "default"

type Store struct {
	db *sql.DB
	// UserID is the default user's id; every request acts as this user for now.
	UserID int64
}

// Open opens (creating if needed) the database in dataDir and applies the schema.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(filepath.Join(dataDir, "compute-box.db")) +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite allows one writer; one connection keeps it simple
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.ensureDefaultUser(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) ensureDefaultUser(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, created_at) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`,
		DefaultUser, utils.Now())
	if err != nil {
		return err
	}
	return s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE name = ?`, DefaultUser).Scan(&s.UserID)
}
