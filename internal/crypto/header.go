package crypto

import (
	"bytes"
	"crypto/subtle"
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
// followed by the AES-256-GCM nonce and ciphertext. The header is the GCM
// additional data, so a changed header fails authentication. A file without
// the magic is format 1 and is read with the legacy parameters. A format 1
// salt can start with the magic by chance (1 in 2^32), so a file that fails
// as format 2 is tried once more as format 1.

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
// reader allocate more than 256 MiB or run Argon2 for more than a few
// seconds before the password check.
const (
	maxTime      = 8
	minMemoryKiB = 8 * 1024
	maxMemoryKiB = 256 * 1024
	maxThreads   = 16
)

// MinNewPasswordLen applies to passwords set now (vault init, bundle export).
// Opening a file accepts whatever password it was written with.
const MinNewPasswordLen = 12

// ErrHeader marks a malformed or out-of-range file header, as opposed to a
// wrong password or a corrupted ciphertext.
var ErrHeader = errors.New("invalid encrypted file header")

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

func (p KDFParams) inRange() bool {
	return p.Time >= 1 && p.Time <= maxTime &&
		p.MemoryKiB >= minMemoryKiB && p.MemoryKiB <= maxMemoryKiB &&
		p.Threads >= 1 && p.Threads <= maxThreads
}

// ParseHeader splits data into its header and the nonce+ciphertext after it.
// It does not resolve the format 1 collision; OpenWithPassword does.
func ParseHeader(data []byte) (Header, []byte, error) {
	if bytes.HasPrefix(data, []byte(headerMagic)) {
		return parseV2(data)
	}
	return parseV1(data)
}

func parseV2(data []byte) (Header, []byte, error) {
	if len(data) < HeaderLen+NonceLen {
		return Header{}, nil, fmt.Errorf("%w: file is truncated", ErrHeader)
	}
	off := len(headerMagic)
	p := KDFParams{
		Time:      binary.BigEndian.Uint32(data[off:]),
		MemoryKiB: binary.BigEndian.Uint32(data[off+4:]),
		Threads:   data[off+8],
	}
	if !p.inRange() {
		return Header{}, nil, fmt.Errorf("%w: out-of-range key parameters (t=%d m=%dKiB p=%d)", ErrHeader, p.Time, p.MemoryKiB, p.Threads)
	}
	salt := append([]byte(nil), data[off+9:HeaderLen]...)
	return Header{Params: p, Salt: salt}, data[HeaderLen:], nil
}

func parseV1(data []byte) (Header, []byte, error) {
	if len(data) < SaltLen+NonceLen {
		return Header{}, nil, fmt.Errorf("%w: file is too short", ErrHeader)
	}
	return Header{Params: LegacyParams, Salt: append([]byte(nil), data[:SaltLen]...), Legacy: true}, data[SaltLen:], nil
}

// DeriveKeyFor derives the AES-256 key for h.
func DeriveKeyFor(password string, h Header) ([]byte, error) {
	if len(h.Salt) != SaltLen {
		return nil, fmt.Errorf("salt must be %d bytes", SaltLen)
	}
	if !h.Legacy && !h.Params.inRange() {
		return nil, fmt.Errorf("%w: out-of-range key parameters", ErrHeader)
	}
	return argon2.IDKey([]byte(password), h.Salt, h.Params.Time, h.Params.MemoryKiB, h.Params.Threads, keyLen), nil
}

// Seal encrypts plaintext with key under h, which must be a format 2 header.
// The header is authenticated as GCM additional data.
func Seal(h Header, key, plaintext []byte) ([]byte, error) {
	if h.Legacy || len(h.Salt) != SaltLen {
		return nil, errors.New("seal needs a format 2 header")
	}
	hb := h.Bytes()
	ct, err := encryptAAD(plaintext, key, hb)
	if err != nil {
		return nil, err
	}
	return append(hb, ct...), nil
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
	defer Zero(key)
	return Seal(h, key, plaintext)
}

// Open decrypts a format 1 or format 2 file and returns the plaintext, the
// header it was read with, and the derived key (the caller owns it).
func Open(data []byte, password string) ([]byte, Header, []byte, error) {
	if !bytes.HasPrefix(data, []byte(headerMagic)) {
		return openWith(data, password, parseV1, nil)
	}
	pt, h, key, err := openWith(data, password, parseV2, data[:min(len(data), HeaderLen)])
	if err == nil {
		return pt, h, key, nil
	}
	// A format 1 salt that starts with the magic. Legacy parameters are
	// fixed, so this costs one bounded Argon2 run.
	if pt, h, key, err1 := openWith(data, password, parseV1, nil); err1 == nil {
		return pt, h, key, nil
	}
	return nil, Header{}, nil, err
}

func openWith(data []byte, password string, parse func([]byte) (Header, []byte, error), aad []byte) ([]byte, Header, []byte, error) {
	h, rest, err := parse(data)
	if err != nil {
		return nil, Header{}, nil, err
	}
	key, err := DeriveKeyFor(password, h)
	if err != nil {
		return nil, Header{}, nil, err
	}
	pt, err := decryptAAD(rest, key, aad)
	if err != nil {
		Zero(key)
		return nil, Header{}, nil, err
	}
	return pt, h, key, nil
}

// OpenWithPassword decrypts a format 1 or format 2 file.
func OpenWithPassword(data []byte, password string) ([]byte, Header, error) {
	pt, h, key, err := Open(data, password)
	Zero(key)
	return pt, h, err
}

// Zero overwrites b. Go may have copied the bytes elsewhere; this only
// shortens how long this copy lives.
func Zero(b []byte) {
	if len(b) > 0 {
		subtle.XORBytes(b, b, b)
	}
}
