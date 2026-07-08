//go:build ignore

// add_user.go — create (or update the password of) a staff account, using the
// database connection from .env. Registration is disabled, so this is how users
// are provisioned.
//
//	go run add_user.go <email> <password>
//	go run add_user.go web@bigtree-group.com 'w3b@BT'
//
// Re-running with an existing email just resets that user's password.
package main

import (
	"context"
	"log"
	"os"
	"strings"

	"bigtree-products/internal/config"
	"bigtree-products/internal/database"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatal("usage: go run add_user.go <email> <password>")
	}
	email := strings.ToLower(strings.TrimSpace(os.Args[1]))
	password := os.Args[2]
	if email == "" || password == "" {
		log.Fatal("email and password are required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("hash: %v", err)
	}

	cfg := config.Load()
	db, err := database.Connect(cfg.DSN)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	// Upsert: new email inserts a row; existing email resets the password.
	_, err = db.ExecContext(context.Background(), `
		INSERT INTO users (id, email, password_hash) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE password_hash = VALUES(password_hash)`,
		uuid.NewString(), email, string(hash))
	if err != nil {
		log.Fatalf("write user: %v", err)
	}

	log.Printf("user %s created/updated", email)
}
