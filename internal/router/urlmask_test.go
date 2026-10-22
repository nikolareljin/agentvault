package router

import (
	"context"
	"strings"
	"testing"
)

// Without the cmd package's masker, a URL with userinfo or a query is
// hidden whole in request errors.
func TestLLMRouterErrorMasksURLByDefault(t *testing.T) {
	const canary = "Canary7pw"
	for _, base := range []string{
		"http://127.0.0.1:1?token=" + canary,
		"http://u:" + canary + "@h\x7f",
	} {
		_, err := callLLMServer(context.Background(), base, "s", "u", "m")
		if err == nil {
			t.Fatalf("%q: no error", base)
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%q: error shows the credential: %v", base, err)
		}
	}
	if got := MaskURL("http://localhost:11434"); got != "http://localhost:11434" {
		t.Errorf("MaskURL hid a plain URL: %q", got)
	}
}
