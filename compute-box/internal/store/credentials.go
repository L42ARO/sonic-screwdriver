package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/utils"
)

// Credential prefixes make a leaked secret recognizable (and easy to scan for).
const (
	machineTokenPrefix = "sonic_mt_"
	apiKeyPrefix       = "sonic_sk_"
	loginTokenPrefix   = "sonic_ln_"
	sessionPrefix      = "sonic_ss_"
)

// newSecret returns a random credential and the hash stored in its place.
func newSecret(prefix string) (secret, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	secret = prefix + base64.RawURLEncoding.EncodeToString(b)
	return secret, hashSecret(secret), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// shown is the part of a secret safe to display later, e.g. "sonic_sk_Ab12…".
func shown(secret string) string { return secret[:min(len(secret), len(apiKeyPrefix)+4)] + "…" }

func expired(ts string) bool {
	t, err := time.Parse(time.RFC3339Nano, ts)
	return err != nil || time.Now().After(t)
}

// --- Machine tokens -------------------------------------------------------------------

// IssueMachineToken creates the machine if needed and gives it a new token, replacing any
// previous one. The token is returned once; only its hash is kept.
func (s *Store) IssueMachineToken(ctx context.Context, userID int64, machine string) (string, error) {
	secret, hash, err := newSecret(machineTokenPrefix)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO machines (user_id, name, token_hash, token_prefix, token_created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, name) DO UPDATE SET token_hash = excluded.token_hash,
			token_prefix = excluded.token_prefix, token_created_at = excluded.token_created_at`,
		userID, machine, hash, shown(secret), utils.Now())
	return secret, err
}

// MachineByToken returns the owner and name of the machine a token belongs to.
func (s *Store) MachineByToken(ctx context.Context, token string) (userID int64, machine string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT user_id, name FROM machines WHERE token_hash = ?`,
		hashSecret(token)).Scan(&userID, &machine)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	return userID, machine, err == nil, err
}

// DeleteMachine removes a machine, its installed programs and its token. Its job history
// is kept. It reports whether the machine existed.
func (s *Store) DeleteMachine(ctx context.Context, userID int64, machine string) (bool, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM machines WHERE user_id = ? AND name = ?`, userID, machine)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// --- API keys -------------------------------------------------------------------------

// APIKey describes a key without revealing it.
type APIKey struct {
	ID         int64
	Name       string
	Prefix     string
	CreatedAt  string
	LastUsedAt string
}

// CreateAPIKey makes a new key for the user and returns it once.
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, name string) (string, error) {
	secret, hash, err := newSecret(apiKeyPrefix)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO api_keys (user_id, name, prefix, hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, shown(secret), hash, utils.Now())
	return secret, err
}

// ListAPIKeys returns the user's active keys, newest first.
func (s *Store) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, prefix, created_at, last_used_at FROM api_keys
		WHERE user_id = ? AND revoked_at IS NULL ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		var used sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &used); err != nil {
			return nil, err
		}
		k.LastUsedAt = used.String
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKey stops a key from working. It reports whether the user had that key.
func (s *Store) RevokeAPIKey(ctx context.Context, userID, keyID int64) (bool, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		utils.Now(), keyID, userID)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// UserByAPIKey returns the owner of an active key and records that it was used.
func (s *Store) UserByAPIKey(ctx context.Context, key string) (int64, bool, error) {
	hash := hashSecret(key)
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM api_keys WHERE hash = ? AND revoked_at IS NULL`, hash).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE hash = ?`, utils.Now(), hash)
	return userID, true, err
}

// --- Dashboard sessions and sign-in links ---------------------------------------------

// CreateSession signs a browser in as the user until ttl passes.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	secret, hash, err := newSecret(sessionPrefix)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions (hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hash, userID, utils.Now(), time.Now().Add(ttl).UTC().Format(time.RFC3339Nano))
	return secret, err
}

// UserBySession returns who a session belongs to, if it is still valid.
func (s *Store) UserBySession(ctx context.Context, session string) (int64, bool, error) {
	var userID int64
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT user_id, expires_at FROM sessions WHERE hash = ?`,
		hashSecret(session)).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil || expired(expires) {
		return 0, false, err
	}
	return userID, true, nil
}

// DeleteSession signs a browser out.
func (s *Store) DeleteSession(ctx context.Context, session string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hashSecret(session))
	return err
}

// CreateLoginToken makes a single-use sign-in token for the user, valid for ttl.
func (s *Store) CreateLoginToken(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	secret, hash, err := newSecret(loginTokenPrefix)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO login_tokens (hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hash, userID, utils.Now(), time.Now().Add(ttl).UTC().Format(time.RFC3339Nano))
	return secret, err
}

// ConsumeLoginToken returns the token's user and marks it used. A token works only once.
func (s *Store) ConsumeLoginToken(ctx context.Context, token string) (int64, bool, error) {
	hash := hashSecret(token)
	var userID int64
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT user_id, expires_at FROM login_tokens WHERE hash = ? AND used_at IS NULL`,
		hash).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil || expired(expires) {
		return 0, false, err
	}
	r, err := s.db.ExecContext(ctx, `UPDATE login_tokens SET used_at = ? WHERE hash = ? AND used_at IS NULL`, utils.Now(), hash)
	if err != nil {
		return 0, false, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return 0, false, nil // used by a concurrent request
	}
	return userID, true, nil
}
