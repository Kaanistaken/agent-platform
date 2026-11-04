// Package config reads the server configuration from environment variables.
// A `.env` file in the repository root (or the working directory) is loaded
// first if present; real environment variables take precedence.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds every setting the server needs. Model keys are deliberately not
// here: users enter their own keys in the UI (D18).
type Config struct {
	Addr      string // listen address of the Go API, e.g. 127.0.0.1:8080
	DBPath    string // SQLite database file
	Origin    string // the browser origin allowed for state-changing requests (D30)
	MasterKey []byte // 32-byte AES-256 key for model keys and tool secrets (D14, D15)
}

// Load reads `.env` files (if any) and the environment.
func Load() (Config, error) {
	// godotenv.Load never overrides variables that are already set.
	for _, p := range []string{".env", "../.env"} {
		if _, err := os.Stat(p); err == nil {
			if err := godotenv.Load(p); err != nil {
				return Config{}, fmt.Errorf("read %s: %w", p, err)
			}
		}
	}

	cfg := Config{
		Addr:   getenv("APP_ADDR", "127.0.0.1:8080"),
		DBPath: getenv("APP_DB_PATH", "data/app.db"),
		Origin: strings.TrimRight(getenv("APP_ORIGIN", "http://localhost:3000"), "/"),
	}

	raw := os.Getenv("APP_MASTER_KEY")
	if raw == "" {
		return Config{}, errors.New("APP_MASTER_KEY is not set (see README: generate 32 random bytes, base64)")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("APP_MASTER_KEY must be 32 bytes, base64-encoded")
	}
	cfg.MasterKey = key
	return cfg, nil
}

func getenv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
