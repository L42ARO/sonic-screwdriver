package store

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PBKDF2-SHA256 at OWASP's recommended iteration count. The count is stored with each
// hash, so it can be raised later without breaking existing passwords.
const passwordIterations = 600_000

// MinPasswordLength is the shortest password SetPassword accepts.
const MinPasswordLength = 10

// SetPassword sets the user's dashboard password and signs out every other browser
// (all sessions except keepSession, which may be empty).
func (s *Store) SetPassword(ctx context.Context, userID int64, password, keepSession string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("passwords are at least %d characters", MinPasswordLength)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	encoded, err := hashPassword(password, salt, passwordIterations)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, encoded, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND hash != ?`, userID, hashSecret(keepSession)); err != nil {
		return err
	}
	return tx.Commit()
}

// HasPassword reports whether the user has set a dashboard password.
func (s *Store) HasPassword(ctx context.Context, userID int64) (bool, error) {
	var stored sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&stored)
	return stored.Valid && stored.String != "", err
}

// CheckPassword reports whether password is the user's dashboard password. A user
// without one never matches.
func (s *Store) CheckPassword(ctx context.Context, userID int64, password string) (bool, error) {
	var stored sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) || !stored.Valid || stored.String == "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// pbkdf2-sha256$<iterations>$<salt>$<hash>
	parts := strings.Split(stored.String, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false, fmt.Errorf("unrecognized password hash for user %d", userID)
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, err
	}
	candidate, err := hashPassword(password, salt, iterations)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(stored.String)) == 1, nil
}

func hashPassword(password string, salt []byte, iterations int) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
