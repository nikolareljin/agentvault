package cmd

import (
	"strings"
	"testing"
)

func TestRedactSensitiveMapDropsCodexHTTPHeaders(t *testing.T) {
	in := map[string]any{"model_providers": map[string]any{"x": map[string]any{
		"base_url":     "https://h.example",
		"http_headers": map[string]any{"X-Org": "plain-header-secret"},
	}}}
	if s := mustJSON(t, redactSensitiveMap(in)); strings.Contains(s, "plain-header-secret") || !strings.Contains(s, "h.example") {
		t.Errorf("http_headers value kept or base_url lost: %s", s)
	}
}
