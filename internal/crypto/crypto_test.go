package crypto

import "testing"

func TestDeriveKey(t *testing.T) {
	key, err := DeriveKey("password", []byte("salt"))
	if err != nil {
		t.Fatalf("DeriveKey() error = %v", err)
	}
	// placeholder returns nil; real implementation will return 32-byte key
	if key != nil {
		t.Errorf("DeriveKey() placeholder should return nil")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	ct, err := Encrypt([]byte("hello"), []byte("key"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if ct != nil {
		t.Errorf("Encrypt() placeholder should return nil")
	}

	pt, err := Decrypt([]byte("cipher"), []byte("key"))
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if pt != nil {
		t.Errorf("Decrypt() placeholder should return nil")
	}
}
