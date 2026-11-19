// Package secretbox encrypts secrets stored in the database with AES-GCM.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"github.com/ysomad/gigafin/internal/errors"
)

// Box seals and opens secrets with one key. A sealed secret is the random nonce
// followed by the ciphertext.
type Box struct {
	aead cipher.AEAD
}

// New returns a box with key, of 16, 24 or 32 bytes.
func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain bound to aad: Open with another aad fails, so a secret copied to
// another row does not open.
func (b *Box) Seal(aad string, plain []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plain, []byte(aad)), nil
}

// Open decrypts a secret sealed by Seal with aad.
func (b *Box) Open(aad string, sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.Error("sealed secret: too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
	if err != nil {
		return nil, fmt.Errorf("open secret: %w", err)
	}
	return plain, nil
}
