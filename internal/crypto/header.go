package crypto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Files written before format 2 start with a bare 16-byte salt, and the
// Argon2id parameters were constants in the code (t=1). Raising them then
// would have made every existing vault and bundle unreadable. Format 2 puts
// the parameters in a header, so they can change without that:
//
//	"AVK2" | time uint32 | memory KiB uint32 | threads uint8 | salt [16]
//
// followed by the AES-256-GCM nonce and ciphertext, as before. A file without
// the magic is format 1 and is read with the legacy parameters.

const headerMagic = "AVK2"

// HeaderLen is the length of a format 2 header.
const HeaderLen = len(headerMagic) + 4 + 4 + 1 + SaltLen

// KDFParams are the Argon2id cost parameters.
type KDFParams struct {
	Time      uint32
	MemoryKiB uint32
	Threads   uint8
}

var (
	// LegacyParams are what format 1 files were written with.
	LegacyParams = KDFParams{Time: argonTime, MemoryKiB: argonMemory, Threads: argonThreads}
	// CurrentParams follow RFC 9106's second recommended option (t=3,
	// 64 MiB, 4 lanes).
	CurrentParams = KDFParams{Time: 3, MemoryKiB: 64 * 1024, Threads: 4}
)

// Bounds on parameters read from a file. A crafted header must not make the
// reader allocate gigabytes or spin for minutes before the password check.
const (
	maxTime      = 10
	minMemoryKiB = 8 * 1024
	maxMemoryKiB = 1024 * 1024
	maxThreads   = 16
)

// Header is the key-derivation header of an encrypted file.
type Header struct {
	Params KDFParams
	Salt   []byte
	// Legacy is true for a format 1 file. Writing it back writes format 2.
	Legacy bool
}

// NewHeader returns a format 2 header with CurrentParams and a fresh salt.
func NewHeader() (Header, error) {
	salt, err := GenerateSalt()
	if err != nil {
		return Header{}, err
	}
	return Header{Params: CurrentParams, Salt: salt}, nil
}

// Bytes encodes h as a format 2 header.
func (h Header) Bytes() []byte {
	b := make([]byte, 0, HeaderLen)
	b = append(b, headerMagic...)
	b = binary.BigEndian.AppendUint32(b, h.Params.Time)
	b = binary.BigEndian.AppendUint32(b, h.Params.MemoryKiB)
	b = append(b, h.Params.Threads)
	return append(b, h.Salt...)
}

// ParseHeader splits data into its header and the nonce+ciphertext after it.
func ParseHeader(data []byte) (Header, []byte, error) {
	if bytes.HasPrefix(data, []byte(headerMagic)) {
		if len(data) < HeaderLen {
			return Header{}, nil, errors.New("encrypted file is truncated")
		}
		off := len(headerMagic)
		p := KDFParams{
			Time:      binary.BigEndian.Uint32(data[off:]),
			MemoryKiB: binary.BigEndian.Uint32(data[off+4:]),
			Threads:   data[off+8],
		}
		if p.Time < 1 || p.Time > maxTime || p.MemoryKiB < minMemoryKiB || p.MemoryKiB > maxMemoryKiB ||
			p.Threads < 1 || p.Threads > maxThreads {
			return Header{}, nil, fmt.Errorf("encrypted file has out-of-range key parameters (t=%d m=%dKiB p=%d)", p.Time, p.MemoryKiB, p.Threads)
		}
		salt := append([]byte(nil), data[off+9:HeaderLen]...)
		return Header{Params: p, Salt: salt}, data[HeaderLen:], nil
	}
	if len(data) < SaltLen {
		return Header{}, nil, errors.New("encrypted file is too short (corrupted?)")
	}
	return Header{Params: LegacyParams, Salt: append([]byte(nil), data[:SaltLen]...), Legacy: true}, data[SaltLen:], nil
}

// DeriveKeyFor derives the AES-256 key for h.
func DeriveKeyFor(password string, h Header) ([]byte, error) {
	if len(h.Salt) == 0 {
		return nil, errors.New("salt must not be empty")
	}
	return argon2.IDKey([]byte(password), h.Salt, h.Params.Time, h.Params.MemoryKiB, h.Params.Threads, keyLen), nil
}

// SealWithPassword encrypts plaintext under a fresh format 2 header.
func SealWithPassword(plaintext []byte, password string) ([]byte, error) {
	h, err := NewHeader()
	if err != nil {
		return nil, err
	}
	key, err := DeriveKeyFor(password, h)
	if err != nil {
		return nil, err
	}
	ct, err := Encrypt(plaintext, key)
	if err != nil {
		return nil, err
	}
	return append(h.Bytes(), ct...), nil
}

// OpenWithPassword decrypts a format 1 or format 2 file.
func OpenWithPassword(data []byte, password string) ([]byte, Header, error) {
	h, rest, err := ParseHeader(data)
	if err != nil {
		return nil, Header{}, err
	}
	key, err := DeriveKeyFor(password, h)
	if err != nil {
		return nil, Header{}, err
	}
	plaintext, err := Decrypt(rest, key)
	if err != nil {
		return nil, Header{}, err
	}
	return plaintext, h, nil
}
