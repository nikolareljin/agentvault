package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

// Every transcript vector validates as its name says, and each invalid one
// fails on the field it was written to fail on.
func TestTranscriptVectors(t *testing.T) {
	wantField := map[string]string{
		"invalid-negative-offset":     "source.offset",
		"invalid-relative-path":       "source.path",
		"invalid-text-without-offset": "source.offset",
	}
	for _, path := range vectorFiles(t, "transcript") {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			var r Record
			readVector(t, path, &r)
			errs := r.Validate()
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

func TestTranscriptValidateChecks(t *testing.T) {
	base := func() Record {
		var r Record
		readVector(t, filepath.Join("testdata", "transcript", "valid-user-text.json"), &r)
		return r
	}
	cases := []struct {
		name   string
		change func(*Record)
		field  string
	}{
		{"role unknown", func(r *Record) { r.Role = "bot" }, "role"},
		{"agent empty", func(r *Record) { r.Agent = "" }, "agent"},
		{"ts local", func(r *Record) { r.TS = "2026-11-09 14:01:58" }, "ts"},
		{"session empty", func(r *Record) { r.SessionID = "" }, "session_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base()
			tc.change(&r)
			errs := r.Validate()
			if len(errs) == 0 || errs[0].Field != tc.field {
				t.Fatalf("want an error on %s, got %v", tc.field, fieldsOf(errs))
			}
		})
	}
}
