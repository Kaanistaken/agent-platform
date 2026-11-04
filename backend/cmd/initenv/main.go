// Command initenv creates or completes the git-ignored .env file in the
// repository root: a random master key and random passwords for the seeded
// users. Existing values are never changed (D31); new passwords are printed
// once so you can log in.
//
//	cd backend && go run ./cmd/initenv
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/kaang/vodafone-case/backend/internal/auth"
)

func main() {
	path := "../.env"
	existing := map[string]bool{}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if k, _, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && !strings.HasPrefix(k, "#") {
				existing[strings.TrimSpace(k)] = true
			}
		}
		f.Close()
	}

	var add []string
	if !existing["APP_MASTER_KEY"] {
		add = append(add, "APP_MASTER_KEY="+random(32))
	}
	for _, u := range auth.DefaultSeed {
		name := "SEED_PASSWORD_" + strings.ToUpper(u.Username)
		if existing[name] {
			continue
		}
		pw := random(12)
		add = append(add, name+"="+pw)
		fmt.Printf("user %-6s (team %s, %s): password %s\n", u.Username, u.Team, u.Role, pw)
	}
	if len(add) == 0 {
		fmt.Println(".env already has every value; nothing changed.")
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, "\n# Added by `go run ./cmd/initenv`. Never commit this file.")
	for _, line := range add {
		fmt.Fprintln(f, line)
	}
	fmt.Printf("wrote %d value(s) to %s\n", len(add), path)
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		log.Fatal(err)
	}
	if n == 32 {
		return base64.StdEncoding.EncodeToString(b) // the 32-byte master key
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
