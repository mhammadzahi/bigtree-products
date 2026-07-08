package config

import (
	"fmt"
	"os"
)

// Config holds runtime configuration sourced from environment variables so the
// same binary runs unchanged across local / staging / production.
type Config struct {
	DSN        string // MariaDB data source name
	ListenAddr string
	SecureCookie bool // set true behind HTTPS so the session cookie gets `Secure`
}

// Load reads configuration from the environment, applying sane local defaults.
func Load() Config {
	return Config{
		DSN:          buildDSN(),
		ListenAddr:   env("LISTEN_ADDR", ":8080"),
		SecureCookie: env("SECURE_COOKIE", "false") == "true",
	}
}

func buildDSN() string {
	if dsn := os.Getenv("DB_DSN"); dsn != "" {
		return dsn
	}
	user := env("DB_USER", "root")
	pass := env("DB_PASS", "")
	host := env("DB_HOST", "127.0.0.1")
	port := env("DB_PORT", "3306")
	name := env("DB_NAME", "bigtree")
	// parseTime lets the driver scan DATETIME/TIMESTAMP into time.Time.
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&loc=Local",
		user, pass, host, port, name)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
