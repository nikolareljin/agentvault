package tui

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestDefaultMaskURLHidesUserinfo(t *testing.T) {
	if got := MaskURL("https://user:pw@host/v1"); got != "<secret>" {
		t.Errorf("MaskURL kept userinfo: %q", got)
	}
	if got := MaskURL("http://localhost:11434"); got != "http://localhost:11434" {
		t.Errorf("MaskURL changed a plain URL: %q", got)
	}
}

func TestMaskURLErrorMasksQuotedURL(t *testing.T) {
	err := maskURLError(&url.Error{Op: "Get", URL: "http://u:pw@h/x", Err: errors.New("refused")})
	if strings.Contains(err.Error(), "pw") {
		t.Errorf("error keeps the credential: %v", err)
	}
}
