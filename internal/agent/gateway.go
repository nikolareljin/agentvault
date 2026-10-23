package agent

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Gateway holds the credentials a reverse-proxy gateway in front of a model
// server checks: a bearer token and optional HTTP basic auth. Gateways are
// kept apart from agents: they are not routed to and generate no config.
type Gateway struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Token         string `json:"token,omitempty"`
	PreviousToken string `json:"previous_token,omitempty"`
	// The user is not a secret and survives an export without keys; the
	// password is blanked like a token.
	BasicAuthUser     string    `json:"basic_auth_user,omitempty"`
	BasicAuthPassword string    `json:"basic_auth_password,omitempty"`
	RotatedAt         time.Time `json:"rotated_at,omitzero"`
	CreatedAt         time.Time `json:"created_at,omitzero"`
	UpdatedAt         time.Time `json:"updated_at,omitzero"`
}

// Validate checks the name, URL and basic auth pair.
func (g Gateway) Validate() error {
	if err := g.ValidateTarget(); err != nil {
		return err
	}
	if (g.BasicAuthUser == "") != (g.BasicAuthPassword == "") {
		return errors.New("basic auth needs both a user and a password")
	}
	if strings.Contains(g.BasicAuthUser, ":") {
		return errors.New("basic auth user must not contain ':'")
	}
	return nil
}

// ValidateTarget checks the name and URL only, for a bundle exported without
// keys (a user with no password). Credentials go in the token and basic auth
// fields, not in the URL, so a URL with a user or password is refused.
func (g Gateway) ValidateTarget() error {
	if strings.TrimSpace(g.Name) == "" {
		return errors.New("gateway name is required")
	}
	if strings.ContainsAny(g.Name, " \t\r\n") {
		return fmt.Errorf("gateway name %q has whitespace", g.Name)
	}
	u, err := url.Parse(strings.TrimSpace(g.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("gateway URL must be http:// or https:// with a host")
	}
	if u.User != nil {
		return errors.New("gateway URL must not carry credentials: use --token and --basic-auth-user/--basic-auth-password")
	}
	return nil
}

// BasicAuth returns "user:pass", or "" when the user or the password is
// not set (an export without keys keeps the user only).
func (g Gateway) BasicAuth() string {
	if g.BasicAuthUser == "" || g.BasicAuthPassword == "" {
		return ""
	}
	return g.BasicAuthUser + ":" + g.BasicAuthPassword
}

// NewGatewayToken returns 32 random bytes as unpadded base64url: 43
// characters that are safe in a header, a URL and an env file.
func NewGatewayToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
