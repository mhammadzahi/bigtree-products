package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
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
	loadDotEnv(".env")
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
	user := os.Getenv("DB_USER")
	pass := os.Getenv("DB_PASS")
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	name := os.Getenv("DB_NAME")
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

// loadDotEnv reads a .env file and sets any keys not already present in the
// environment. Values are taken LITERALLY — no shell-style `$var` expansion —
// so passwords containing `$` are preserved. Missing file is not an error.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // no .env: rely on the real environment
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Strip a single layer of surrounding quotes if present.
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}
