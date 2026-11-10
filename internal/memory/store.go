package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // pure Go SQLite with FTS5, no cgo
)

// DBFileName is the database file under the data directory.
const DBFileName = "memory.db"

// hitsPerSource caps the hits one source contributes to a search.
const hitsPerSource = 20

// Snippet markers: control characters the stored text is cleaned of on
// output and that cannot be typed into a query, swapped for [ and ] in the
// JSON body so a marked word is plain text to every client.
const markOpen, markClose = "\x01", "\x02"

// ErrInvalid wraps the field errors of a row the contract refuses.
type ErrInvalid struct {
	Errors []FieldError
}

func (e *ErrInvalid) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		parts = append(parts, fe.Field+": "+fe.Detail)
	}
	return "the contract rejects the row: " + strings.Join(parts, "; ")
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS entries (
    seq         INTEGER PRIMARY KEY,
    id          TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL CHECK (kind IN ('decision', 'idea', 'prompt', 'note', 'task-state')),
    repo_code   TEXT,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    agent       TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    state       TEXT NOT NULL CHECK (state IN ('inbox', 'exported', 'landed')),
    exported_pr TEXT,
    provenance  TEXT NOT NULL CHECK (provenance IN ('cli', 'hook', 'api')),
    CHECK ((state = 'inbox') = (exported_pr IS NULL))
);
CREATE TABLE IF NOT EXISTS transcripts (
    id            INTEGER PRIMARY KEY,
    session_id    TEXT NOT NULL,
    agent         TEXT NOT NULL,
    ts            TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system', 'tool')),
    text          TEXT,
    source_path   TEXT NOT NULL,
    source_offset INTEGER,
    CHECK (text IS NULL OR source_offset IS NOT NULL),
    UNIQUE (source_path, source_offset)
);
CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
    title, body, content='entries', content_rowid='seq');
CREATE VIRTUAL TABLE IF NOT EXISTS transcripts_fts USING fts5(
    text, content='transcripts', content_rowid='id');
CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
    INSERT INTO entries_fts(rowid, title, body) VALUES (new.seq, new.title, new.body);
END;
CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, title, body) VALUES ('delete', old.seq, old.title, old.body);
END;
CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE OF title, body ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, title, body) VALUES ('delete', old.seq, old.title, old.body);
    INSERT INTO entries_fts(rowid, title, body) VALUES (new.seq, new.title, new.body);
END;
CREATE TRIGGER IF NOT EXISTS transcripts_ai AFTER INSERT ON transcripts BEGIN
    INSERT INTO transcripts_fts(rowid, text) VALUES (new.id, new.text);
END;
CREATE TRIGGER IF NOT EXISTS transcripts_ad AFTER DELETE ON transcripts BEGIN
    INSERT INTO transcripts_fts(transcripts_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;
CREATE TRIGGER IF NOT EXISTS transcripts_au AFTER UPDATE OF text ON transcripts BEGIN
    INSERT INTO transcripts_fts(transcripts_fts, rowid, text) VALUES ('delete', old.id, old.text);
    INSERT INTO transcripts_fts(rowid, text) VALUES (new.id, new.text);
END;
`

// Every statement is a constant with ? placeholders; no query text is
// ever built from a value.
const (
	entryColumns   = "id, kind, repo_code, title, body, session_id, agent, created_at, state, exported_pr, provenance"
	insertEntrySQL = `INSERT INTO entries (id, kind, repo_code, title, body, session_id, agent, created_at, state,
		exported_pr, provenance) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	selectEntrySQL  = "SELECT " + entryColumns + " FROM entries WHERE id = ?"
	listEntriesSQL  = "SELECT " + entryColumns + " FROM entries ORDER BY seq"
	listByStateSQL  = "SELECT " + entryColumns + " FROM entries WHERE state = ? ORDER BY seq"
	insertRecordSQL = `INSERT OR IGNORE INTO transcripts (session_id, agent, ts, role, text, source_path, source_offset)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	searchEntriesSQL = `SELECT bm25(entries_fts), snippet(entries_fts, -1, ?, ?, '...', 16), e.id, e.kind, e.repo_code,
		e.title, e.body, e.session_id, e.agent, e.created_at, e.state, e.exported_pr, e.provenance
		FROM entries_fts JOIN entries e ON e.seq = entries_fts.rowid WHERE entries_fts MATCH ?
		ORDER BY bm25(entries_fts) LIMIT ?`
	searchRecordsSQL = `SELECT bm25(transcripts_fts), snippet(transcripts_fts, -1, ?, ?, '...', 16), t.session_id,
		t.agent, t.ts, t.role, t.text, t.source_path, t.source_offset
		FROM transcripts_fts JOIN transcripts t ON t.id = transcripts_fts.rowid WHERE transcripts_fts MATCH ?
		ORDER BY bm25(transcripts_fts) LIMIT ?`
	insertMetaSQL = "INSERT INTO meta (key, value) VALUES ('schema_version', ?)"
	selectMetaSQL = "SELECT value FROM meta WHERE key = ?"
)

// Store is the one writer of the database file.
type Store struct {
	db   *sql.DB
	path string
}

// DefaultDBPath is $XDG_DATA_HOME/agentvault/memory.db, else
// ~/.local/share/agentvault/memory.db. A relative XDG_DATA_HOME is
// invalid by the XDG spec and ignored.
func DefaultDBPath() (string, error) {
	if xdg := filepath.Clean(os.Getenv("XDG_DATA_HOME")); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "agentvault", DBFileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("memory: no home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "agentvault", DBFileName), nil
}

// Open creates or opens the database at path. The parent directory is
// created mode 0700 and the file is set to 0600: it holds transcripts. A
// path inside a git worktree is refused so the file cannot be committed.
func Open(path string) (*Store, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if insideWorktree(filepath.Dir(path)) {
		return nil, fmt.Errorf("memory: %s is inside a git worktree; use a path outside every checkout", path)
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	// The file exists, mode 0600, before the driver opens it: SQLite would
	// otherwise create it (and its -wal and -shm) under the process umask.
	if err := privateFile(path); err != nil {
		return nil, err
	}
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: one writer, no lock contention between the pool's
	// connections, and PRAGMAs apply to the connection every query uses.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: %s: not a usable database: %w", path, err)
	}
	for _, side := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(side, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.Close()
			return nil, err
		}
	}
	have, err := s.metaGet("schema_version")
	if err != nil {
		db.Close()
		return nil, err
	}
	switch have {
	case "":
		if _, err := db.Exec(insertMetaSQL, SchemaVersion); err != nil {
			db.Close()
			return nil, err
		}
	case SchemaVersion:
	default:
		db.Close()
		return nil, fmt.Errorf("memory: %s: schema_version %s, this build knows %s", path, have, SchemaVersion)
	}
	return s, nil
}

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) metaGet(key string) (string, error) {
	var value string
	err := s.db.QueryRow(selectMetaSQL, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// privateDir creates dir mode 0700, or narrows an existing one that other
// users could enter.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("memory: %s is not a directory", dir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		// #nosec G302 -- a directory needs the owner's execute bit; 0700 is the tightest usable mode
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("memory: %s is mode %04o and cannot be narrowed: %w", dir, info.Mode().Perm(), err)
		}
	}
	return nil
}

// privateFile creates path empty, mode 0600, when it does not exist, and
// narrows an existing one. A path that is not a regular file is refused.
func privateFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	f.Close()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("memory: %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return os.Chmod(path, 0o600)
	}
	return nil
}

// insideWorktree reports whether dir or an ancestor holds a .git entry.
func insideWorktree(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// NewEntry is the inbox row POST /entries stores for what the client sent:
// id, created_at, state, exported_pr and provenance are the server's.
type NewEntry struct {
	Kind      string
	RepoCode  *string
	Title     string
	Body      string
	SessionID string
	Agent     string
}

// InsertEntry writes one inbox row with the given provenance and returns it
// as stored. The row is checked against memory.entry@1 first; the CHECK
// constraints are a second net, not the rule.
func (s *Store) InsertEntry(n NewEntry, provenance string) (Entry, error) {
	e := Entry{
		SchemaVersion: SchemaVersion, ID: NewULID(), Kind: n.Kind, RepoCode: n.RepoCode,
		Title: strings.TrimSpace(n.Title), Body: n.Body, SessionID: n.SessionID, Agent: n.Agent,
		CreatedAt: UTCNow(), State: "inbox", ExportedPR: nil, Provenance: provenance,
	}
	if errs := e.Validate(); errs != nil {
		return Entry{}, &ErrInvalid{Errors: errs}
	}
	_, err := s.db.Exec(insertEntrySQL, e.ID, e.Kind, e.RepoCode, e.Title, e.Body, e.SessionID, e.Agent, e.CreatedAt, e.State,
		e.ExportedPR, e.Provenance)
	if err != nil {
		return Entry{}, err
	}
	row := s.db.QueryRow(selectEntrySQL, e.ID)
	return scanEntry(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(row scanner) (Entry, error) {
	var e Entry
	e.SchemaVersion = SchemaVersion
	err := row.Scan(&e.ID, &e.Kind, &e.RepoCode, &e.Title, &e.Body, &e.SessionID, &e.Agent,
		&e.CreatedAt, &e.State, &e.ExportedPR, &e.Provenance)
	return e, err
}

// ListEntries returns the rows in insertion order, all states when state
// is empty.
func (s *Store) ListEntries(state string) ([]Entry, error) {
	var rows *sql.Rows
	var err error
	if state == "" {
		rows, err = s.db.Query(listEntriesSQL)
	} else {
		rows, err = s.db.Query(listByStateSQL, state)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertTranscript stores one transcript line for a future search. A line
// already stored (same path and offset) is left as it is.
func (s *Store) InsertTranscript(rec Record) error {
	if rec.SchemaVersion == "" {
		rec.SchemaVersion = SchemaVersion
	}
	if errs := rec.Validate(); errs != nil {
		return &ErrInvalid{Errors: errs}
	}
	_, err := s.db.Exec(insertRecordSQL, rec.SessionID, rec.Agent, rec.TS, rec.Role, rec.Text, rec.Source.Path,
		rec.Source.Offset)
	return err
}

// Hit is one search result, in the contract's search_result shape.
type Hit struct {
	Source  string  `json:"source"`
	Rank    float64 `json:"rank"`
	Entry   *Entry  `json:"entry,omitempty"`
	Record  *Record `json:"record,omitempty"`
	Snippet string  `json:"snippet"`
}

var wordPattern = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// ftsQuery turns free text into an FTS5 query: every word as a quoted
// phrase, AND-ed. User text never reaches the FTS parser as syntax (no
// NEAR, no column filters, no *). Empty when the text has no word.
func ftsQuery(text string) string {
	words := wordPattern.FindAllString(text, 16)
	if len(words) == 0 {
		return ""
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " ")
}

// Search returns the FTS5 hits over entries and transcripts, best rank
// first (bm25: lower is better). Snippets mark matches with [ and ].
func (s *Store) Search(text string) ([]Hit, error) {
	match := ftsQuery(text)
	hits := []Hit{}
	if match == "" {
		return hits, nil
	}
	rows, err := s.db.Query(searchEntriesSQL, markOpen, markClose, match, hitsPerSource)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h Hit
		var e Entry
		e.SchemaVersion = SchemaVersion
		if err := rows.Scan(&h.Rank, &h.Snippet, &e.ID, &e.Kind, &e.RepoCode, &e.Title, &e.Body, &e.SessionID,
			&e.Agent, &e.CreatedAt, &e.State, &e.ExportedPR, &e.Provenance); err != nil {
			rows.Close()
			return nil, err
		}
		h.Source, h.Entry, h.Snippet = "entry", &e, plainMarks(h.Snippet)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = s.db.Query(searchRecordsSQL, markOpen, markClose, match, hitsPerSource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h Hit
		r := Record{SchemaVersion: SchemaVersion}
		if err := rows.Scan(&h.Rank, &h.Snippet, &r.SessionID, &r.Agent, &r.TS, &r.Role, &r.Text,
			&r.Source.Path, &r.Source.Offset); err != nil {
			return nil, err
		}
		h.Source, h.Record, h.Snippet = "transcript", &r, plainMarks(h.Snippet)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Stable: a tie keeps entries before transcripts.
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Rank < hits[j].Rank })
	return hits, nil
}

func plainMarks(snippet string) string {
	return strings.NewReplacer(markOpen, "[", markClose, "]").Replace(snippet)
}
