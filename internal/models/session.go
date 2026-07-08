package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// SessionTTL is how long a login session stays valid.
const SessionTTL = 7 * 24 * time.Hour

// CreateSession issues a cryptographically-random opaque session token and
// persists it. The token — never the user id — is what lives in the cookie.
func CreateSession(ctx context.Context, db *sql.DB, userID string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	_, err := db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, time.Now().Add(SessionTTL))
	if err != nil {
		return "", err
	}
	return token, nil
}

// UserIDForSession returns the owning user id for a still-valid session token.
func UserIDForSession(ctx context.Context, db *sql.DB, token string) (string, error) {
	if token == "" {
		return "", errors.New("empty session token")
	}
	var userID string
	err := db.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE token = ? AND expires_at > NOW()`, token).
		Scan(&userID)
	if err != nil {
		return "", err
	}
	return userID, nil
}

// DeleteSession revokes a session (logout).
func DeleteSession(ctx context.Context, db *sql.DB, token string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}
