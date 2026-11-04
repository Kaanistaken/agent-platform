package crypto

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/kaang/vodafone-case/backend/internal/db"
)

// D14, D31: authenticated encryption bound to the row identity.
func TestSealOpenBindsIdentity(t *testing.T) {
	b, _ := New(bytes.Repeat([]byte{7}, 32))
	ct, nonce, err := b.Seal([]byte("sk-secret"), ModelKeyAAD(1, "openai"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("sk-secret")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	if pt, err := b.Open(ct, nonce, ModelKeyAAD(1, "openai")); err != nil || string(pt) != "sk-secret" {
		t.Fatalf("open: %q %v", pt, err)
	}
	// Moving the ciphertext to another user's row fails.
	if _, err := b.Open(ct, nonce, ModelKeyAAD(2, "openai")); err == nil {
		t.Fatal("ciphertext opened under another identity")
	}
	_, nonce2, _ := b.Seal([]byte("sk-secret"), ModelKeyAAD(1, "openai"))
	if bytes.Equal(nonce, nonce2) {
		t.Fatal("nonce reused")
	}
}

// D31: the server refuses to start with a wrong master key.
func TestVerifyMasterKey(t *testing.T) {
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	right, _ := New(bytes.Repeat([]byte{1}, 32))
	wrong, _ := New(bytes.Repeat([]byte{2}, 32))
	if err := right.VerifyMasterKey(context.Background(), d.W); err != nil {
		t.Fatal(err)
	}
	if err := right.VerifyMasterKey(context.Background(), d.W); err != nil {
		t.Fatalf("same key refused: %v", err)
	}
	if err := wrong.VerifyMasterKey(context.Background(), d.W); err == nil {
		t.Fatal("wrong master key accepted")
	}
}
