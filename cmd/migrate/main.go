// migrate loads one or more .sql files into the database using the SAME
// connection settings as the server — i.e. read straight from .env. No local
// mysql client and no password prompt required.
//
//	go run ./cmd/migrate schema.sql
//	go run ./cmd/migrate schema.sql seed.sql
package main

import (
	"log"
	"os"
	"strings"

	"bigtree-products/internal/config"
	"bigtree-products/internal/database"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: migrate <file.sql> [more.sql ...]")
	}

	cfg := config.Load() // loads .env, builds the DSN

	// multiStatements lets a whole .sql file run in one Exec call.
	dsn := cfg.DSN
	if strings.Contains(dsn, "?") {
		dsn += "&multiStatements=true"
	} else {
		dsn += "?multiStatements=true"
	}

	db, err := database.Connect(dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	for _, path := range os.Args[1:] {
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read %s: %v", path, err)
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			log.Fatalf("exec %s: %v", path, err)
		}
		log.Printf("applied %s", path)
	}
	log.Println("done")
}
