// Package crypto provides authenticated encryption for the AgentVault.
//
// Security design:
//   - Key derivation: Argon2id (memory-hard, resistant to GPU/ASIC attacks)
//   - Encryption: AES-256-GCM authenticated encryption
//   - Random nonces: crypto/rand for all nonce/salt generation
//
// Files written now carry their Argon2id parameters in a format 2 header
// (see header.go). The constants below are the format 1 parameters, kept so
// files from earlier releases still open.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	// Format 1 Argon2id parameters. New files use CurrentParams.
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	keyLen       = 32 // AES-256

	// SaltLen is the byte length of a random salt.
	SaltLen = 16
	// NonceLen is the byte length of an AES-GCM nonce.
	NonceLen = 12
)

// GenerateSalt returns a cryptographically random salt.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}
	return salt, nil
}

// DeriveKey derives an AES-256 key with the format 1 parameters. New code
// uses DeriveKeyFor with a Header.
func DeriveKey(password string, salt []byte) ([]byte, error) {
	if len(salt) == 0 {
		return nil, errors.New("salt must not be empty")
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, keyLen)
	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns nonce + ciphertext (nonce is prepended).
func Encrypt(plaintext, key []byte) ([]byte, error) {
	return encryptAAD(plaintext, key, nil)
}

// encryptAAD is Encrypt with additional authenticated data.
func encryptAAD(plaintext, key, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, aad)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM.
// Expects nonce prepended to the ciphertext (as produced by Encrypt).
func Decrypt(ciphertext, key []byte) ([]byte, error) {
	return decryptAAD(ciphertext, key, nil)
}

// decryptAAD is Decrypt with additional authenticated data.
func decryptAAD(ciphertext, key, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}
	return plaintext, nil
}
