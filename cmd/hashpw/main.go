// hashpw generates a bcrypt hash and a ready-to-run INSERT statement so you can
// provision staff accounts manually in the database (registration is disabled).
//
//	go run ./cmd/hashpw 'someone@bigtree-group.com' 'SuperSecret123'
//
// Arguments: <email> <password>
package main

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: hashpw <email> <password>")
		os.Exit(1)
	}
	email, password := os.Args[1], os.Args[2]

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash error:", err)
		os.Exit(1)
	}

	id := uuid.NewString()

	fmt.Printf("\nbcrypt hash:\n%s\n\n", hash)
	fmt.Printf("SQL to insert this user:\n")
	fmt.Printf("INSERT INTO users (id, email, password_hash)\n")
	fmt.Printf("VALUES ('%s', '%s', '%s');\n\n", id, email, hash)
}
