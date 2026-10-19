package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A settings file that can hold a token used to be redacted whole without
// --include-secrets, so the other machine got no settings.json at all: no
// hooks, no permissions, no plugins. Field-level redaction blanks only the
// values that look like secrets, records where they were, and on import
// fills them back from the file already on that machine, or leaves them out.

// secretKeyParts mark a JSON key whose value is a secret.
var secretKeyParts = []string{
	"token", "secret", "password", "passwd", "apikey", "api_key", "api-key",
	"authorization", "bearer", "credential", "private_key", "privatekey",
	"access_key", "accesskey", "session_key", "client_secret", "cookie",
}

// secretKeys are short keys that are secrets by themselves.
var secretKeys = map[string]bool{"key": true, "auth": true, "pat": true, "pass": true}

// secretContainers hold values that are commonly secrets: an MCP server's env
// and an HTTP server's headers. Every string under them is blanked.
var secretContainers = map[string]bool{"env": true, "headers": true}

func isSecretKey(k string) bool {
	lower := strings.ToLower(k)
	if secretKeys[lower] {
		return true
	}
	for _, p := range secretKeyParts {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// redactJSONSecrets blanks every secret-looking value in a JSON object and
// returns the new document and the JSON Pointers (RFC 6901) of the blanked
// values. ok is false when data is not a JSON object; the caller then
// redacts the whole file as before.
func redactJSONSecrets(data []byte) (out []byte, pointers []string, ok bool) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, nil, false
	}
	var walk func(v any, ptr string, under bool) any
	walk = func(v any, ptr string, under bool) any {
		switch t := v.(type) {
		case map[string]any:
			for k, inner := range t {
				t[k] = walk(inner, ptr+"/"+escapePointer(k), under || isSecretKey(k) || secretContainers[strings.ToLower(k)])
			}
			return t
		case []any:
			for i, inner := range t {
				t[i] = walk(inner, ptr+"/"+strconv.Itoa(i), under)
			}
			return t
		case string, json.Number:
			if under {
				pointers = append(pointers, ptr)
				return ""
			}
			return t
		default:
			return t
		}
	}
	walk(doc, "", false)
	sort.Strings(pointers)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, false
	}
	return out, pointers, true
}

// restoreRedactedFields fills each redacted pointer in incoming from the same
// pointer in existing, the file already on this machine. A pointer existing
// does not have is removed, so a tool never reads an empty string as its
// token.
func restoreRedactedFields(incoming []byte, pointers []string, existing []byte) ([]byte, int, error) {
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(incoming))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, 0, fmt.Errorf("bundle content is not a JSON object: %w", err)
	}
	var have map[string]any
	if len(bytes.TrimSpace(existing)) > 0 {
		d := json.NewDecoder(bytes.NewReader(existing))
		d.UseNumber()
		if err := d.Decode(&have); err != nil {
			have = nil
		}
	}
	restored := 0
	// Deepest first, so removing an array element does not shift a sibling
	// pointer that is still to be handled.
	sorted := append([]string(nil), pointers...)
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	for _, p := range sorted {
		if v, ok := pointerGet(have, p); ok {
			pointerSet(doc, p, v)
			restored++
			continue
		}
		pointerDelete(doc, p)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	return out, restored, err
}

func escapePointer(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}

func splitPointer(p string) []string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts
}

func pointerGet(doc map[string]any, p string) (any, bool) {
	var cur any = doc
	for _, part := range splitPointer(p) {
		switch t := cur.(type) {
		case map[string]any:
			v, ok := t[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(t) {
				return nil, false
			}
			cur = t[i]
		default:
			return nil, false
		}
	}
	return cur, cur != nil
}

// pointerParent returns the container holding the last segment of p.
func pointerParent(doc map[string]any, p string) (any, string) {
	parts := splitPointer(p)
	var cur any = doc
	for _, part := range parts[:len(parts)-1] {
		switch t := cur.(type) {
		case map[string]any:
			cur = t[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(t) {
				return nil, ""
			}
			cur = t[i]
		default:
			return nil, ""
		}
	}
	return cur, parts[len(parts)-1]
}

func pointerSet(doc map[string]any, p string, v any) {
	parent, last := pointerParent(doc, p)
	switch t := parent.(type) {
	case map[string]any:
		t[last] = v
	case []any:
		if i, err := strconv.Atoi(last); err == nil && i >= 0 && i < len(t) {
			t[i] = v
		}
	}
}

// pointerDelete removes a map key. An array element is left as "" rather
// than removed, so the positions of its siblings do not change.
func pointerDelete(doc map[string]any, p string) {
	parent, last := pointerParent(doc, p)
	if m, ok := parent.(map[string]any); ok {
		delete(m, last)
	}
}
