package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// FuzzMergeMCPServers checks that the rebuilt object is valid JSON and equals
// the input with mcpServers replaced. The seeds run in every `go test`.
func FuzzMergeMCPServers(f *testing.F) {
	for _, s := range []string{
		`{}`, ` { } `, `{"a":1}`, `{"mcpServers":5,"b":[1,{"c":null}]}`,
		`{"a":"é\n\"<&>","a":2}`, `{"mcpServers":{},"z":true}`,
		`{"x":{"y":{"z":[[[]]]}},"n":1.50e+3}`, `{"k":"😀"}`,
		`{"a":1} {"b":2}`, `{"a":1} x`, `[1]`, `null`, `"s"`,
		"{\"a\":\"\xff\"}", `{"a":1,}`, strings.Repeat(`{"a":`, 200) + `1` + strings.Repeat(`}`, 200),
	} {
		f.Add(s)
	}
	gen := []byte(`{"mcpServers":{"s":{"command":"c"}}}`)
	f.Fuzz(func(t *testing.T, in string) {
		p := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(p, []byte(in), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := mergeMCPServersIntoFile(p, gen)
		if err != nil {
			return
		}
		if !json.Valid(out) {
			t.Fatalf("invalid JSON for %q: %s", in, out)
		}
		want := map[string]any{}
		trimmed := bytes.TrimPrefix([]byte(in), []byte("\xef\xbb\xbf"))
		if len(bytes.TrimSpace(trimmed)) != 0 {
			if err := decodeNum(trimmed, &want); err != nil {
				t.Fatalf("accepted %q that does not decode: %v", in, err)
			}
		}
		var g map[string]any
		_ = decodeNum(gen, &g)
		want["mcpServers"] = g["mcpServers"]
		var got map[string]any
		if err := decodeNum(out, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("for %q got %v want %v", in, got, want)
		}
	})
}

func decodeNum(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}
