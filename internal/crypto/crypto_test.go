package crypto

import (
	"bytes"
	"testing"
)

func TestGenerateSalt(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt() error = %v", err)
	}
	if len(salt) != SaltLen {
		t.Errorf("GenerateSalt() len = %d, want %d", len(salt), SaltLen)
	}
	// two calls should produce different salts
	salt2, _ := GenerateSalt()
	if bytes.Equal(salt, salt2) {
		t.Error("GenerateSalt() produced identical salts")
	}
}

func TestDeriveKey(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key, err := DeriveKey("password", salt)
	if err != nil {
		t.Fatalf("DeriveKey() error = %v", err)
	}
	if len(key) != 32 {
		t.Errorf("DeriveKey() key len = %d, want 32", len(key))
	}
	// same inputs produce same key
	key2, _ := DeriveKey("password", salt)
	if !bytes.Equal(key, key2) {
		t.Error("DeriveKey() not deterministic for same inputs")
	}
	// different password produces different key
	key3, _ := DeriveKey("other", salt)
	if bytes.Equal(key, key3) {
		t.Error("DeriveKey() same key for different passwords")
	}
}

func TestDeriveKeyEmptySalt(t *testing.T) {
	_, err := DeriveKey("password", nil)
	if err == nil {
		t.Error("DeriveKey() expected error for empty salt")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	plaintext := []byte("hello, agentvault!")

	ct, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Error("Encrypt() ciphertext equals plaintext")
	}

	pt, err := Decrypt(ct, key)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("Decrypt() = %q, want %q", pt, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key := make([]byte, 32)
	ct, _ := Encrypt([]byte("secret"), key)

	wrongKey := make([]byte, 32)
	wrongKey[0] = 1
	_, err := Decrypt(ct, wrongKey)
	if err == nil {
		t.Error("Decrypt() expected error for wrong key")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key := make([]byte, 32)
	_, err := Decrypt([]byte("short"), key)
	if err == nil {
		t.Error("Decrypt() expected error for short ciphertext")
	}
}

func TestEncryptDecryptEmpty(t *testing.T) {
	key := make([]byte, 32)
	ct, err := Encrypt([]byte{}, key)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	pt, err := Decrypt(ct, key)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if len(pt) != 0 {
		t.Errorf("Decrypt() len = %d, want 0", len(pt))
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("same input")
	ct1, _ := Encrypt(plaintext, key)
	ct2, _ := Encrypt(plaintext, key)
	if bytes.Equal(ct1, ct2) {
		t.Error("Encrypt() produced identical ciphertext for same input (nonce reuse)")
	}
}

func TestRoundTripWithDerivedKey(t *testing.T) {
	salt, _ := GenerateSalt()
	key, _ := DeriveKey("my-master-password", salt)

	plaintext := []byte(`{"agents":[]}`)
	ct, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	pt, err := Decrypt(ct, key)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("round-trip failed: got %q, want %q", pt, plaintext)
	}
}
