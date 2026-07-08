package models

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ErrEmailTaken is returned when registering an already-registered email.
var ErrEmailTaken = errors.New("email already registered")

// ErrInvalidCredentials is returned for a failed login (unknown email OR bad password).
// The same error is used for both cases to avoid leaking which emails exist.
var ErrInvalidCredentials = errors.New("invalid email or password")

// User is a staff account. Every account has the same access — there are no
// roles (all users are company staff).
type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// CreateUser hashes the password with bcrypt and inserts a new account.
func CreateUser(ctx context.Context, db *sql.DB, email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return nil, errors.New("email and password are required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	u := &User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: string(hash),
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES (?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash)
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 { // duplicate key
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return u, nil
}

// Authenticate verifies an email/password pair and returns the user on success.
func Authenticate(ctx context.Context, db *sql.DB, email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u := &User{}
	err := db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Run a dummy compare to keep timing roughly constant against enumeration.
		bcrypt.CompareHashAndPassword([]byte("$2a$10$0000000000000000000000000000000000000000000000000000"), []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// GetUserByID loads a user by primary key (used by the session middleware).
func GetUserByID(ctx context.Context, db *sql.DB, id string) (*User, error) {
	u := &User{}
	err := db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}
