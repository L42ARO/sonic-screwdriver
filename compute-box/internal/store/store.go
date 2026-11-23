// Package store keeps the compute-box state in SQLite: users, credentials, projects,
// operations, machines, jobs, job steps and model usage. Every row belongs to a user, and
// every method that reads or changes owned data takes that user explicitly.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// migrations are applied in file-name order, each exactly once. 001 uses IF NOT EXISTS so
// databases created before migrations existed adopt it without changes.
//
//go:embed migrations/*.sql
var migrations embed.FS

// DefaultUser is the owner of a self-hosted compute-box.
const DefaultUser = "default"

type Store struct {
	db            *sql.DB
	defaultUserID int64
}

// Open opens (creating if needed) the database in dataDir and brings its schema up to date.
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
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if s.defaultUserID, err = s.EnsureUser(context.Background(), DefaultUser); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DefaultUserID is the self-hosted owner's id.
func (s *Store) DefaultUserID() int64 { return s.defaultUserID }

// EnsureUser returns the id of the user with this name, creating the user if needed.
func (s *Store) EnsureUser(ctx context.Context, name string) (int64, error) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, created_at) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`,
		name, utils.Now()); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE name = ?`, name).Scan(&id)
	return id, err
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", name)
		}
		var applied int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, utils.Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
