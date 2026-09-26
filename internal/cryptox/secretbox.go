// Package cryptox provides at-rest encryption for account credentials
// stored in SQLite. See PLAN.md §0 (AES-256-GCM, key from
// XMAIL_ENCRYPTION_KEY).
package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Encrypt encrypts plain using AES-256-GCM under key (must be exactly
// 32 bytes), returning the ciphertext and the random nonce used. The
// nonce must be stored alongside the ciphertext and passed to Decrypt.
func Encrypt(key, plain []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("cryptox: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("cryptox: new gcm: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("cryptox: generate nonce: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plain, nil)
	return ciphertext, nonce, nil
}

// Decrypt reverses Encrypt: given the same key and the nonce returned
// by Encrypt, it recovers the original plaintext. Returns an error if
// key/nonce/ciphertext do not match (tampered or wrong key).
func Decrypt(key, ciphertext, nonce []byte) (plain []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cryptox: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptox: new gcm: %w", err)
	}
	plain, err = gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("cryptox: decrypt: %w", err)
	}
	return plain, nil
}
