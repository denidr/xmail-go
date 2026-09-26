package cryptox

import (
	"bytes"
	"testing"
)

func testKey() []byte {
	return bytes.Repeat([]byte{0x42}, 32)
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	cases := []string{
		"",
		"hunter2",
		"a very long app-password with special chars !@#$%^&*()",
	}
	key := testKey()
	for _, plain := range cases {
		ciphertext, nonce, err := Encrypt(key, []byte(plain))
		if err != nil {
			t.Fatalf("Encrypt(%q) error = %v", plain, err)
		}
		got, err := Decrypt(key, ciphertext, nonce)
		if err != nil {
			t.Fatalf("Decrypt(%q) error = %v", plain, err)
		}
		if string(got) != plain {
			t.Errorf("roundtrip = %q, want %q", got, plain)
		}
	}
}

func TestEncrypt_NonceIsRandomPerCall(t *testing.T) {
	key := testKey()
	c1, n1, err := Encrypt(key, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	c2, n2, err := Encrypt(key, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Equal(n1, n2) {
		t.Error("nonce reused across calls, must be random per call")
	}
	if bytes.Equal(c1, c2) {
		t.Error("ciphertext identical across calls despite same plaintext, nonce not actually randomizing output")
	}
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	ciphertext, nonce, err := Encrypt(testKey(), []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	wrongKey := bytes.Repeat([]byte{0x99}, 32)
	if _, err := Decrypt(wrongKey, ciphertext, nonce); err == nil {
		t.Error("Decrypt() with wrong key succeeded, want error")
	}
}

func TestDecrypt_WrongNonceFails(t *testing.T) {
	key := testKey()
	ciphertext, nonce, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	tamperedNonce := make([]byte, len(nonce))
	copy(tamperedNonce, nonce)
	tamperedNonce[0] ^= 0xFF
	if _, err := Decrypt(key, ciphertext, tamperedNonce); err == nil {
		t.Error("Decrypt() with tampered nonce succeeded, want error")
	}
}

func TestDecrypt_TamperedCiphertextFails(t *testing.T) {
	key := testKey()
	ciphertext, nonce, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[0] ^= 0xFF
	if _, err := Decrypt(key, tampered, nonce); err == nil {
		t.Error("Decrypt() with tampered ciphertext succeeded, want error (GCM auth tag should catch this)")
	}
}

// TestDecrypt_WrongLengthNonceErrorsNotPanics: gcm.Open panics when the nonce
// is not its NonceSize, and the stored nonce is just a BLOB column — a row
// written by another tool or version must surface as an error, not a crash.
func TestDecrypt_WrongLengthNonceErrorsNotPanics(t *testing.T) {
	key := testKey()
	ciphertext, _, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	for _, nonce := range [][]byte{nil, {}, make([]byte, 8), make([]byte, 16)} {
		plain, err := Decrypt(key, ciphertext, nonce)
		if err == nil {
			t.Errorf("Decrypt(nonce of %d bytes) = %q, want error", len(nonce), plain)
		}
		if plain != nil {
			t.Errorf("Decrypt(nonce of %d bytes) returned plaintext %q, want nil", len(nonce), plain)
		}
	}
}

func TestEncrypt_InvalidKeyLength(t *testing.T) {
	if _, _, err := Encrypt([]byte("tooshort"), []byte("data")); err == nil {
		t.Error("Encrypt() with non-32-byte key succeeded, want error")
	}
}
