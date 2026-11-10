package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// vectorFiles lists testdata/<dir>/*.json, sorted.
func vectorFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no vectors under testdata/%s", dir)
	}
	sort.Strings(files)
	return files
}

func readVector(t *testing.T, path string, into any) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return data
}

func fieldsOf(errs []FieldError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Field)
	}
	return out
}

// Every entry vector validates as its name says, and each invalid one
// fails on the field it was written to fail on.
func TestEntryVectors(t *testing.T) {
	wantField := map[string]string{
		"invalid-inbox-with-exported-pr":     "exported_pr",
		"invalid-landed-without-exported-pr": "exported_pr",
		"invalid-missing-provenance":         "provenance",
		"invalid-repo-named-not-coded":       "repo_code",
	}
	for _, path := range vectorFiles(t, "entry") {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			var e Entry
			var raw map[string]any
			readVector(t, path, &e)
			readVector(t, path, &raw)
			for name := range raw {
				if !contains(EntryFields, name) {
					t.Fatalf("vector has a field the contract lacks: %s", name)
				}
			}
			errs := e.Validate()
			if strings.HasPrefix(name, "valid-") {
				if errs != nil {
					t.Fatalf("valid vector refused: %v", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != wantField[name] {
				t.Fatalf("want one error on %s, got %v", wantField[name], fieldsOf(errs))
			}
		})
	}
}

func TestEntryValidateChecks(t *testing.T) {
	base := func() Entry {
		var e Entry
		readVector(t, filepath.Join("testdata", "entry", "valid-inbox-decision.json"), &e)
		return e
	}
	long := strings.Repeat("x", TitleMaxLength)
	cases := []struct {
		name   string
		change func(*Entry)
		field  string
	}{
		{"schema_version 2", func(e *Entry) { e.SchemaVersion = "2" }, "schema_version"},
		{"id lowercase", func(e *Entry) { e.ID = strings.ToLower(e.ID) }, "id"},
		{"id with I", func(e *Entry) { e.ID = "01JCA4N7Q8ZKX2VD5HTW3BRM9I" }, "id"},
		{"kind unknown", func(e *Entry) { e.Kind = "todo" }, "kind"},
		{"repo code two digits", func(e *Entry) { s := "R-12"; e.RepoCode = &s }, "repo_code"},
		{"title empty", func(e *Entry) { e.Title = "" }, "title"},
		{"title 201 runes", func(e *Entry) { e.Title = long + "y" }, "title"},
		{"title newline", func(e *Entry) { e.Title = "a\nb" }, "title"},
		{"session empty", func(e *Entry) { e.SessionID = "" }, "session_id"},
		{"agent uppercase", func(e *Entry) { e.Agent = "Claude" }, "agent"},
		{"agent 33 chars", func(e *Entry) { e.Agent = "a" + strings.Repeat("b", 32) }, "agent"},
		{"created_at offset", func(e *Entry) { e.CreatedAt = "2026-11-09T14:02:11+01:00" }, "created_at"},
		{"created_at month 13", func(e *Entry) { e.CreatedAt = "2026-13-09T14:02:11Z" }, "created_at"},
		{"state unknown", func(e *Entry) { e.State = "done" }, "state"},
		{"exported_pr not github", func(e *Entry) {
			e.State = "exported"
			s := "https://example.com/pull/1"
			e.ExportedPR = &s
		}, "exported_pr"},
		{"provenance unknown", func(e *Entry) { e.Provenance = "web" }, "provenance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base()
			tc.change(&e)
			errs := e.Validate()
			if len(errs) == 0 || errs[0].Field != tc.field {
				t.Fatalf("want an error on %s, got %v", tc.field, fieldsOf(errs))
			}
			for _, fe := range errs {
				if !regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`).MatchString(fe.Code) {
					t.Fatalf("code %q is not an error.problem@1 code", fe.Code)
				}
			}
		})
	}
	ok := base()
	ok.Title = long + "  " // 200 runes plus the whitespace the server trims
	ok.Title = strings.TrimSpace(ok.Title)
	multi := strings.Repeat("é", TitleMaxLength) // 200 code points, 400 bytes
	for _, title := range []string{long, multi} {
		e := base()
		e.Title = title
		if errs := e.Validate(); errs != nil {
			t.Fatalf("title of %d code points refused: %v", TitleMaxLength, errs)
		}
	}
}

func TestNewULID(t *testing.T) {
	seen := map[string]bool{}
	var prev string
	for i := 0; i < 200; i++ {
		id := NewULID()
		if !ulidPattern.MatchString(id) {
			t.Fatalf("not a ULID: %q", id)
		}
		if seen[id] {
			t.Fatalf("repeated ULID %s", id)
		}
		seen[id] = true
		// Same millisecond or later: the time prefix never goes backwards.
		if prev != "" && id[:10] < prev[:10] {
			t.Fatalf("time prefix went backwards: %s after %s", id, prev)
		}
		prev = id
	}
	// The first ten characters are the millisecond timestamp.
	before := time.Now().UnixMilli()
	id := NewULID()
	var ms uint64
	for _, c := range id[:10] {
		ms = ms<<5 | uint64(strings.IndexRune(ulidAlphabet, c))
	}
	if int64(ms) < before || int64(ms) > time.Now().UnixMilli() {
		t.Fatalf("ULID time %d is not now (%d)", ms, before)
	}
}

func TestUTCNowMatchesContract(t *testing.T) {
	if now := UTCNow(); !timestampPattern.MatchString(now) {
		t.Fatalf("UTCNow() = %q does not match the contract pattern", now)
	}
}
