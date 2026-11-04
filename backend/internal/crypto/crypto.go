// Package crypto encrypts model keys and tool secrets at rest.
//
// AES-256-GCM from the standard library (D14), a fresh random nonce for every
// encryption, and associated data built from immutable row identities so a
// ciphertext cannot be moved to another row (D31).
package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

// KeyVersion is the version of the master key in use (D15). Rotation is not
// implemented; the column exists so it can be added later.
const KeyVersion = 1

// AADVersion is the version of the associated-data format below (D31).
const AADVersion = 1

// Box encrypts and decrypts with the master key.
type Box struct {
	aead cipher.AEAD
}

// New creates a Box from a 32-byte master key.
func New(masterKey []byte) (*Box, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext bound to aad and returns ciphertext and nonce.
func (b *Box) Seal(plaintext []byte, aad string) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return b.aead.Seal(nil, nonce, plaintext, []byte(aad)), nonce, nil
}

// Open decrypts ciphertext that was sealed with the same aad.
func (b *Box) Open(ciphertext, nonce []byte, aad string) ([]byte, error) {
	if len(nonce) != b.aead.NonceSize() {
		return nil, errors.New("invalid nonce")
	}
	return b.aead.Open(nil, nonce, ciphertext, []byte(aad))
}

// ModelKeyAAD is the associated data of a user's model key.
func ModelKeyAAD(userID int64, provider string) string {
	return fmt.Sprintf("v%d|model_key|user=%d|provider=%s", AADVersion, userID, provider)
}

// SecretAAD is the associated data of a tool secret. The secret ID is never
// reused, and name and host cannot change after creation (D54).
func SecretAAD(secretID, userID int64) string {
	return fmt.Sprintf("v%d|secret|id=%d|user=%d", AADVersion, secretID, userID)
}

const checkPlaintext = "vodafone-case key check"

func checkAAD() string { return fmt.Sprintf("v%d|key_check|version=%d", AADVersion, KeyVersion) }

// VerifyMasterKey refuses a wrong master key (D31): on first start it stores
// a known value; on every later start it must decrypt that value.
func (b *Box) VerifyMasterKey(ctx context.Context, w *sql.DB) error {
	var ct, nonce []byte
	err := w.QueryRowContext(ctx, "SELECT ciphertext, nonce FROM key_check WHERE key_version = ?", KeyVersion).
		Scan(&ct, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		ct, nonce, err := b.Seal([]byte(checkPlaintext), checkAAD())
		if err != nil {
			return err
		}
		_, err = w.ExecContext(ctx, "INSERT INTO key_check (key_version, ciphertext, nonce, aad_version) VALUES (?, ?, ?, ?)",
			KeyVersion, ct, nonce, AADVersion)
		return err
	}
	if err != nil {
		return err
	}
	pt, err := b.Open(ct, nonce, checkAAD())
	if err != nil || string(pt) != checkPlaintext {
		return errors.New("APP_MASTER_KEY does not match the key this database was created with")
	}
	return nil
}
