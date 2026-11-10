package memory

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikolareljin/agentvault/internal/safefile"
)

// TokenFileName is the bearer token file, next to the database.
const TokenFileName = "memory.token"

// tokenMinLength is shorter than anything this code writes (43 characters);
// a shorter file is not one this tool wrote.
const tokenMinLength = 32

// LoadOrCreateToken returns the per-install bearer token from
// <dir>/memory.token, writing 32 random bytes (URL-safe base64) mode 0600 on
// first start. A file other users can read is refused rather than trusted.
func LoadOrCreateToken(dir string) (string, error) {
	path := filepath.Join(dir, TokenFileName)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := f.WriteString(base64.RawURLEncoding.EncodeToString(raw) + "\n")
			cerr := f.Close()
			if werr != nil || cerr != nil {
				os.Remove(path)
				return "", errors.Join(werr, cerr)
			}
		} else if !errors.Is(err, os.ErrExist) {
			// os.ErrExist: a second server starting at the same moment wrote it first.
			return "", err
		}
	}
	if err := checkTokenMode(path); err != nil {
		return "", err
	}
	data, err := safefile.ReadMax(path, 4096)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if len(token) < tokenMinLength || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("memory: %s is not a token this tool wrote; delete it and start again", path)
	}
	return token, nil
}
