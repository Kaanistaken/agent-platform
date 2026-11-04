package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaang/vodafone-case/backend/internal/db"
)

// D16, D31: seeding reports missing variables by name, never resets an
// existing password and rejects passwords bcrypt would truncate.
func TestSeed(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	users := []SeedUser{{"erin", "gamma", "lead"}}

	t.Setenv("SEED_PASSWORD_ERIN", "")
	if err := Seed(ctx, d.W, users); err == nil || !strings.Contains(err.Error(), "SEED_PASSWORD_ERIN") {
		t.Fatalf("missing password: %v", err)
	}
	t.Setenv("SEED_PASSWORD_ERIN", strings.Repeat("x", 73))
	if err := Seed(ctx, d.W, users); err == nil {
		t.Fatal("73-byte password accepted")
	}
	t.Setenv("SEED_PASSWORD_ERIN", "first-password")
	if err := Seed(ctx, d.W, users); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEED_PASSWORD_ERIN", "second-password")
	if err := Seed(ctx, d.W, users); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(ctx, d.W, "erin", "first-password"); err != nil {
		t.Fatalf("password was reset: %v", err)
	}
	if _, err := Login(ctx, d.W, "erin", "second-password"); err != ErrBadCredentials {
		t.Fatalf("new password accepted: %v", err)
	}

	tok, _ := Login(ctx, d.W, "erin", "first-password")
	if u, ok := Authenticate(ctx, d.W, tok); !ok || u.Username != "erin" || u.Role != "lead" {
		t.Fatalf("authenticate: %+v %v", u, ok)
	}
	var stored []byte
	d.W.QueryRow("SELECT token_hash FROM sessions LIMIT 1").Scan(&stored)
	if strings.Contains(string(stored), tok) {
		t.Fatal("session token stored in clear")
	}
	Logout(ctx, d.W, tok)
	if _, ok := Authenticate(ctx, d.W, tok); ok {
		t.Fatal("session valid after logout")
	}
}
