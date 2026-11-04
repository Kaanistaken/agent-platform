// Package auth handles passwords, seeded users and server-side sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kaang/vodafone-case/backend/internal/db"
)

// SessionTTL is how long a login stays valid (value chosen under D85).
const SessionTTL = 12 * time.Hour

// User is the authenticated caller. Team and role always come from the
// database, never from the request (D45).
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	TeamID   int64  `json:"team_id"`
	TeamName string `json:"team_name"`
	Role     string `json:"role"`
}

// IsLead reports whether the user is a team lead.
func (u User) IsLead() bool { return u.Role == "lead" }

// SeedUser describes one predefined user. The password comes from the
// environment variable SEED_PASSWORD_<USERNAME> (D16).
type SeedUser struct {
	Username string
	Team     string
	Role     string
}

// DefaultSeed defines two teams with a lead and a member each (R6, D44).
var DefaultSeed = []SeedUser{
	{"alice", "alpha", "lead"},
	{"bob", "alpha", "member"},
	{"carol", "beta", "lead"},
	{"dave", "beta", "member"},
}

// Seed creates missing teams and users. Existing users are never changed, so
// passwords are never reset (D31). Missing variables are reported by name only.
func Seed(ctx context.Context, w *sql.DB, users []SeedUser) error {
	var missing []string
	for _, u := range users {
		var exists int
		if err := w.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = ?", u.Username).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		envName := "SEED_PASSWORD_" + strings.ToUpper(u.Username)
		pw := os.Getenv(envName)
		if pw == "" {
			missing = append(missing, envName)
			continue
		}
		hash, err := HashPassword(pw)
		if err != nil {
			return fmt.Errorf("%s: %w", envName, err)
		}
		if _, err := w.ExecContext(ctx, "INSERT OR IGNORE INTO teams (name) VALUES (?)", u.Team); err != nil {
			return err
		}
		if _, err := w.ExecContext(ctx, `INSERT INTO users (team_id, username, password_hash, role, created_at)
			SELECT id, ?, ?, ?, ? FROM teams WHERE name = ?`, u.Username, hash, u.Role, db.Now(), u.Team); err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing seed passwords: %s", strings.Join(missing, ", "))
	}
	return nil
}

// HashPassword hashes with bcrypt. Passwords over bcrypt's 72-byte limit are
// rejected, never truncated (D31).
func HashPassword(pw string) (string, error) {
	if len(pw) > 72 {
		return "", errors.New("password longer than 72 bytes")
	}
	if len(pw) < 8 {
		return "", errors.New("password shorter than 8 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

// dummyHash makes a login with an unknown username cost the same time as one
// with a wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)

// Login checks the credentials and creates a session. It returns the session
// token, which is given to the browser only; the database stores its hash (D38).
func Login(ctx context.Context, w *sql.DB, username, password string) (string, error) {
	var id int64
	var hash string
	err := w.QueryRowContext(ctx, "SELECT id, password_hash FROM users WHERE username = ?", username).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", ErrBadCredentials
	}
	if err != nil {
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", ErrBadCredentials
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	if _, err := w.ExecContext(ctx, "INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)",
		hashToken(token), id, db.Format(now), db.Format(now.Add(SessionTTL))); err != nil {
		return "", err
	}
	// Expired sessions are removed opportunistically.
	w.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", db.Format(now))
	return token, nil
}

// ErrBadCredentials is returned for an unknown user or a wrong password.
var ErrBadCredentials = errors.New("invalid username or password")

// Logout deletes the session server-side (D30).
func Logout(ctx context.Context, w *sql.DB, token string) error {
	_, err := w.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hashToken(token))
	return err
}

// Authenticate returns the user of a valid, unexpired session.
func Authenticate(ctx context.Context, r *sql.DB, token string) (User, bool) {
	if token == "" {
		return User{}, false
	}
	var u User
	err := r.QueryRowContext(ctx, `SELECT u.id, u.username, u.team_id, t.name, u.role
		FROM sessions s JOIN users u ON u.id = s.user_id JOIN teams t ON t.id = u.team_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, hashToken(token), db.Now()).
		Scan(&u.ID, &u.Username, &u.TeamID, &u.TeamName, &u.Role)
	if err != nil {
		return User{}, false
	}
	return u, true
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
