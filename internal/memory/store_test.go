package memory

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data", DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func strp(s string) *string { return &s }

func TestOpenCreatesPrivateFile(t *testing.T) {
	s := openTestStore(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("database mode %04o, want 0600", mode)
	}
	dir, _ := os.Stat(filepath.Dir(s.Path()))
	if mode := dir.Mode().Perm(); mode != 0o700 {
		t.Fatalf("data dir mode %04o, want 0700", mode)
	}
}

func TestOpenRefusesWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Open(filepath.Join(root, "sub", DBFileName))
	if err == nil || !strings.Contains(err.Error(), "git worktree") {
		t.Fatalf("want a worktree refusal, got %v", err)
	}
}

func TestOpenRefusesOtherSchemaVersion(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("UPDATE meta SET value = '2' WHERE key = 'schema_version'"); err != nil {
		t.Fatal(err)
	}
	path := s.Path()
	s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "schema_version 2") {
		t.Fatalf("want a schema_version refusal, got %v", err)
	}
}

func TestOpenRefusesNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), DBFileName)
	if err := os.WriteFile(path, []byte("not sqlite at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "not a usable database") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

func TestInsertAndListEntries(t *testing.T) {
	s := openTestStore(t)
	stored, err := s.InsertEntry(NewEntry{
		Kind: "decision", RepoCode: strp("R-765"), Title: "  Keep transcripts local  ",
		Body: "Search reads them over loopback only.", SessionID: "s1", Agent: "claude",
	}, "api")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != "Keep transcripts local" {
		t.Fatalf("title not trimmed: %q", stored.Title)
	}
	if stored.State != "inbox" || stored.ExportedPR != nil || stored.Provenance != "api" {
		t.Fatalf("server-owned fields wrong: %+v", stored)
	}
	if errs := stored.Validate(); errs != nil {
		t.Fatalf("stored row fails the contract: %v", errs)
	}
	for _, state := range []string{"", "inbox"} {
		got, err := s.ListEntries(state)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !reflect.DeepEqual(got[0], stored) {
			t.Fatalf("ListEntries(%q) = %+v, want the stored row", state, got)
		}
	}
	got, err := s.ListEntries("landed")
	if err != nil || len(got) != 0 {
		t.Fatalf("ListEntries(landed) = %v, %v; want none", got, err)
	}
}

func TestInsertEntryRefusesContractViolation(t *testing.T) {
	s := openTestStore(t)
	_, err := s.InsertEntry(NewEntry{Kind: "decision", RepoCode: strp("not-a-code"), Title: "t",
		SessionID: "s", Agent: "claude"}, "api")
	var invalid *ErrInvalid
	if !errors.As(err, &invalid) || len(invalid.Errors) != 1 || invalid.Errors[0].Field != "repo_code" {
		t.Fatalf("want ErrInvalid on repo_code, got %v", err)
	}
	rows, _ := s.ListEntries("")
	if len(rows) != 0 {
		t.Fatalf("a refused row was stored: %+v", rows)
	}
}

func TestInsertTranscriptAndSearch(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.InsertEntry(NewEntry{Kind: "note", Title: "Rebinding guard",
		Body: "The Host header check stops DNS rebinding from a browser tab.", SessionID: "s1", Agent: "codex"}, "api"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertEntry(NewEntry{Kind: "idea", Title: "Unrelated",
		Body: "Nothing about networks here.", SessionID: "s1", Agent: "codex"}, "api"); err != nil {
		t.Fatal(err)
	}
	offset := int64(10)
	rec := Record{SessionID: "s1", Agent: "codex", TS: "2026-11-09T14:01:58Z", Role: "user",
		Text: strp("Please keep the rebinding check."), Source: Source{Path: "/tmp/x.jsonl", Offset: &offset}}
	if err := s.InsertTranscript(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTranscript(rec); err != nil {
		t.Fatalf("second insert of the same line: %v", err)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM transcripts").Scan(&n); err != nil || n != 1 {
		t.Fatalf("transcripts count %d (%v), want 1", n, err)
	}
	if err := s.InsertTranscript(Record{SessionID: "s1", Agent: "codex", TS: "x", Role: "user",
		Source: Source{Path: "/tmp/x.jsonl"}}); err == nil {
		t.Fatal("invalid record stored")
	}

	hits, err := s.Search("rebinding")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %+v", hits)
	}
	var sources []string
	for _, h := range hits {
		sources = append(sources, h.Source)
		if !strings.Contains(h.Snippet, "[rebinding]") && !strings.Contains(h.Snippet, "[Rebinding]") {
			t.Fatalf("snippet lacks a marked match: %q", h.Snippet)
		}
		if strings.ContainsAny(h.Snippet, markOpen+markClose) {
			t.Fatalf("raw marker in snippet: %q", h.Snippet)
		}
		if h.Rank >= 0 {
			t.Fatalf("bm25 rank %v is not negative", h.Rank)
		}
		switch h.Source {
		case "entry":
			if h.Entry == nil || h.Record != nil || h.Entry.Title != "Rebinding guard" {
				t.Fatalf("entry hit malformed: %+v", h)
			}
		case "transcript":
			if h.Record == nil || h.Entry != nil || h.Record.Source.Offset == nil || *h.Record.Source.Offset != 10 {
				t.Fatalf("transcript hit malformed: %+v", h)
			}
			if errs := h.Record.Validate(); errs != nil {
				t.Fatalf("transcript hit fails its contract: %v", errs)
			}
		}
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Rank < hits[i-1].Rank {
			t.Fatalf("hits not best-first: %v", hits)
		}
	}
	if len(sources) != 2 || sources[0] == sources[1] {
		t.Fatalf("want one entry and one transcript hit, got %v", sources)
	}
	// Both words must match: "rebinding networks" hits nothing.
	hits, err = s.Search("rebinding networks")
	if err != nil || len(hits) != 0 {
		t.Fatalf("AND semantics broken: %v %v", hits, err)
	}
	// Punctuation and FTS syntax are words or nothing, never operators.
	for _, q := range []string{"", "   ", "\"", "NEAR(", "title:guard", "rebind*", "- OR -"} {
		if _, err := s.Search(q); err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
	}
}

func TestFTSQuery(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"plain words":      `"plain" "words"`,
		`say "hi" NEAR(x)`: `"say" "hi" "NEAR" "x"`,
		"café résumé":      `"café" "résumé"`,
		"a-b":              `"a" "b"`,
	}
	for in, want := range cases {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ftsQuery(strings.Repeat("w ", 40)); strings.Count(got, `"w"`) != 16 {
		t.Fatalf("word cap not 16: %q", got)
	}
}
