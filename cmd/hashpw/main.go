// hashpw generates a bcrypt hash and a ready-to-run INSERT statement so you can
// provision users manually in the database (registration is disabled).
//
//	go run ./cmd/hashpw 'admin@bigtree-group.com' 'SuperSecret123' admin
//
// Arguments: <email> <password> [role]   role defaults to "buyer".
package main

import (
	"crypto/rand"
	"fmt"
	"os"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: hashpw <email> <password> [role]")
		os.Exit(1)
	}
	email, password := os.Args[1], os.Args[2]
	role := "buyer"
	if len(os.Args) > 3 {
		role = os.Args[3]
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash error:", err)
		os.Exit(1)
	}

	id := uuid.NewString()
	_ = rand.Reader // uuid already uses crypto/rand

	fmt.Printf("\nbcrypt hash:\n%s\n\n", hash)
	fmt.Printf("SQL to insert this user:\n")
	fmt.Printf("INSERT INTO users (id, email, password_hash, role)\n")
	fmt.Printf("VALUES ('%s', '%s', '%s', '%s');\n\n", id, email, hash, role)
}
