package crypto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

// legacySeal writes the format 1 layout: bare salt, then nonce+ciphertext,
// keyed with the constants every earlier release used.
func legacySeal(t *testing.T, plaintext []byte, password string) []byte {
	t.Helper()
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	key, err := DeriveKey(password, salt)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	return append(salt, ct...)
}

func TestSealOpenFormat2(t *testing.T) {
	data, err := SealWithPassword([]byte("hello"), "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte(headerMagic)) {
		t.Fatal("no format 2 header")
	}
	got, h, err := OpenWithPassword(data, "correct horse")
	if err != nil || string(got) != "hello" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if h.Legacy || h.Params != CurrentParams {
		t.Errorf("header = %+v", h)
	}
	if _, _, err := OpenWithPassword(data, "wrong"); err == nil {
		t.Error("wrong password accepted")
	}
}

// Every file written before format 2 must still open.
func TestOpenFormat1(t *testing.T) {
	data := legacySeal(t, []byte("old"), "pw-from-before")
	got, h, err := OpenWithPassword(data, "pw-from-before")
	if err != nil || string(got) != "old" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if !h.Legacy || h.Params != LegacyParams {
		t.Errorf("header = %+v, want legacy", h)
	}
}

// A crafted header must be refused before Argon2 runs with its numbers.
func TestParseHeaderRefusesOutOfRangeParams(t *testing.T) {
	for _, p := range []KDFParams{
		{Time: 0, MemoryKiB: 64 * 1024, Threads: 4},
		{Time: 4_000_000_000, MemoryKiB: 64 * 1024, Threads: 4},
		{Time: 3, MemoryKiB: 64 * 1024 * 1024, Threads: 4},
		{Time: 3, MemoryKiB: 64 * 1024, Threads: 0},
	} {
		h := Header{Params: p, Salt: make([]byte, SaltLen)}
		start := time.Now()
		if _, _, err := ParseHeader(append(h.Bytes(), make([]byte, 40)...)); err == nil {
			t.Errorf("%+v accepted", p)
		}
		if time.Since(start) > time.Second {
			t.Errorf("%+v took %v to refuse", p, time.Since(start))
		}
	}
	short := []byte(headerMagic)
	short = binary.BigEndian.AppendUint32(short, 3)
	if _, _, err := ParseHeader(short); err == nil {
		t.Error("truncated header accepted")
	}
}

// The header is GCM additional data: the ciphertext does not open without it.
func TestHeaderIsAuthenticated(t *testing.T) {
	h, err := NewHeader()
	if err != nil {
		t.Fatal(err)
	}
	key, err := DeriveKeyFor("correct horse", h)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Seal(h, key, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(data[HeaderLen:], key); err == nil {
		t.Error("ciphertext opened without the header as additional data")
	}
	tampered := append([]byte(nil), data...)
	tampered[len(headerMagic)+3] ^= 1 // time 3 -> 2, still in range
	if _, _, err := OpenWithPassword(tampered, "correct horse"); err == nil {
		t.Error("tampered header accepted")
	}
}

// A format 1 salt that happens to start with the magic still opens.
func TestOpenFormat1WithMagicSalt(t *testing.T) {
	salt := append([]byte(headerMagic), make([]byte, SaltLen-len(headerMagic))...)
	salt[len(headerMagic)] = 0xff // makes the "time" field out of range
	key, err := DeriveKey("pw-from-before", salt)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Encrypt([]byte("old"), key)
	if err != nil {
		t.Fatal(err)
	}
	got, h, err := OpenWithPassword(append(salt, ct...), "pw-from-before")
	if err != nil || string(got) != "old" || !h.Legacy {
		t.Fatalf("open = %q, %+v, %v", got, h, err)
	}
}

func TestParseHeaderBounds(t *testing.T) {
	ok := []KDFParams{
		{Time: maxTime, MemoryKiB: maxMemoryKiB, Threads: maxThreads},
		{Time: 1, MemoryKiB: minMemoryKiB, Threads: 1},
	}
	bad := []KDFParams{
		{Time: 9, MemoryKiB: 64 * 1024, Threads: 4}, // literals pin the caps
		{Time: 3, MemoryKiB: 256*1024 + 1, Threads: 4},
		{Time: 3, MemoryKiB: 8*1024 - 1, Threads: 4},
		{Time: 3, MemoryKiB: 64 * 1024, Threads: 17},
	}
	for _, p := range ok {
		h := Header{Params: p, Salt: make([]byte, SaltLen)}
		if _, _, err := ParseHeader(append(h.Bytes(), make([]byte, 40)...)); err != nil {
			t.Errorf("%+v refused: %v", p, err)
		}
	}
	for _, p := range bad {
		h := Header{Params: p, Salt: make([]byte, SaltLen)}
		_, _, err := ParseHeader(append(h.Bytes(), make([]byte, 40)...))
		if !errors.Is(err, ErrHeader) {
			t.Errorf("%+v: err = %v, want ErrHeader", p, err)
		}
		if _, err := DeriveKeyFor("pw", h); err == nil {
			t.Errorf("%+v: DeriveKeyFor ran", p)
		}
	}
	if _, err := DeriveKeyFor("pw", Header{Params: CurrentParams, Salt: make([]byte, 8)}); err == nil {
		t.Error("short salt accepted")
	}
}

// New vaults use RFC 9106's second recommended option. Tests elsewhere swap
// CurrentParams for cheap ones (cryptotest), so this package, which does not,
// is where a change to the shipped values has to show up.
func TestCurrentParamsAreRFC9106(t *testing.T) {
	want := KDFParams{Time: 3, MemoryKiB: 64 * 1024, Threads: 4}
	if CurrentParams != want {
		t.Fatalf("CurrentParams = %+v, want %+v", CurrentParams, want)
	}
}
