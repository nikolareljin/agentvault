package crypto

// DeriveKey derives an AES-256 key from a password using Argon2id.
func DeriveKey(password string, salt []byte) ([]byte, error) {
	return nil, nil // placeholder: will use golang.org/x/crypto/argon2
}

// Encrypt encrypts plaintext using AES-256-GCM.
func Encrypt(plaintext, key []byte) ([]byte, error) {
	return nil, nil // placeholder
}

// Decrypt decrypts ciphertext using AES-256-GCM.
func Decrypt(ciphertext, key []byte) ([]byte, error) {
	return nil, nil // placeholder
}
