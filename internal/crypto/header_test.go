package crypto

import (
	"bytes"
	"encoding/binary"
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
