// Package memory is the memory.api@1 server: a loopback-only HTTP API over
// one SQLite file that holds the work-memory inbox (decisions, ideas,
// prompts, notes, task states an agent wrote during a session) and the
// agent transcripts an importer stores next to it.
//
// Every rule here comes from the contract's three schemas. The server owns
// id, created_at, state, exported_pr and provenance of a posted row; the
// client picks kind, repo_code, title, body, session_id and agent.
package memory

import (
	"crypto/rand"
	"encoding/binary"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// SchemaVersion is the memory.entry@1 and memory.transcript.record@1 version.
const SchemaVersion = "1"

// TitleMaxLength is the contract's maxLength for a title, in code points.
const TitleMaxLength = 200

// Entry is one memory.entry@1 row.
type Entry struct {
	SchemaVersion string  `json:"schema_version"`
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	RepoCode      *string `json:"repo_code"`
	Title         string  `json:"title"`
	Body          string  `json:"body"`
	SessionID     string  `json:"session_id"`
	Agent         string  `json:"agent"`
	CreatedAt     string  `json:"created_at"`
	State         string  `json:"state"`
	ExportedPR    *string `json:"exported_pr"`
	Provenance    string  `json:"provenance"`
}

// FieldError is one contract violation, as the 422 body lists them.
type FieldError struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// EntryFields are the entry's property names, in the contract's order.
var EntryFields = []string{"schema_version", "id", "kind", "repo_code", "title", "body",
	"session_id", "agent", "created_at", "state", "exported_pr", "provenance"}

// ServerOwnedFields are the entry fields POST /entries sets itself, whatever
// the client sent.
var ServerOwnedFields = []string{"id", "created_at", "state", "exported_pr", "provenance"}

// Kinds, States and Provenances are the contract's enums.
var (
	Kinds       = []string{"decision", "idea", "prompt", "note", "task-state"}
	States      = []string{"inbox", "exported", "landed"}
	Provenances = []string{"cli", "hook", "api"}
)

var (
	ulidPattern      = regexp.MustCompile(`^[0-9ABCDEFGHJKMNPQRSTVWXYZ]{26}$`)
	repoCodePattern  = regexp.MustCompile(`^R-[0-9]{3,}$`)
	agentPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	timestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,6})?(Z|\+00:00)$`)
	prURLPattern     = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+$`)
	// A title is one line: no newline, tab or other control character.
	controlChars = regexp.MustCompile("[\x00-\x1f\x7f]")
)

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID mints a ULID: 48 bits of Unix milliseconds, then 80 random bits,
// Crockford base32. Sortable by creation time, unique with no coordination.
func NewULID() string {
	var raw [16]byte
	var stamp [8]byte
	big.NewInt(time.Now().UnixMilli()).FillBytes(stamp[:])
	copy(raw[:6], stamp[2:])
	if _, err := rand.Read(raw[6:]); err != nil {
		panic("memory: crypto/rand failed: " + err.Error())
	}
	// 128 bits into 26 base32 characters, least significant last.
	hi := binary.BigEndian.Uint64(raw[:8])
	lo := binary.BigEndian.Uint64(raw[8:])
	out := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		out[i] = ulidAlphabet[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out)
}

// UTCNow is created_at as the contract writes it: UTC, seconds, Z.
func UTCNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// Validate returns every memory.entry@1 violation of e, sorted by field, or
// nil. The checks are the schema's, written out: const, enum, pattern,
// minLength, maxLength, and the if/then/else on state and exported_pr.
func (e Entry) Validate() []FieldError {
	var errs []FieldError
	add := func(field, code, detail string) {
		errs = append(errs, FieldError{Field: field, Code: code, Detail: detail})
	}
	if e.SchemaVersion != SchemaVersion {
		add("schema_version", "CONST", "must be \""+SchemaVersion+"\"")
	}
	if !ulidPattern.MatchString(e.ID) {
		add("id", "PATTERN", "must be a 26-character Crockford base32 ULID")
	}
	if !contains(Kinds, e.Kind) {
		add("kind", "ENUM", "must be one of "+strings.Join(Kinds, ", "))
	}
	if e.RepoCode != nil && !repoCodePattern.MatchString(*e.RepoCode) {
		add("repo_code", "PATTERN", "must be a repository code R-NNN, not a name, or null")
	}
	if n := utf8.RuneCountInString(e.Title); n < 1 {
		add("title", "MIN_LENGTH", "must not be empty")
	} else if n > TitleMaxLength {
		add("title", "MAX_LENGTH", "must be at most 200 characters")
	}
	if controlChars.MatchString(e.Title) {
		add("title", "CONTROL_CHARACTER", "must be one line of printable text; put the rest in body")
	}
	if !utf8.ValidString(e.Title) {
		add("title", "ENCODING", "must be UTF-8")
	}
	if !utf8.ValidString(e.Body) {
		add("body", "ENCODING", "must be UTF-8")
	}
	if e.SessionID == "" {
		add("session_id", "MIN_LENGTH", "must not be empty")
	}
	if !agentPattern.MatchString(e.Agent) {
		add("agent", "PATTERN", "must match ^[a-z][a-z0-9-]{0,31}$")
	}
	if !timestampPattern.MatchString(e.CreatedAt) {
		add("created_at", "PATTERN", "must be a UTC date-time, for example 2026-01-02T03:04:05Z")
	} else if _, err := time.Parse(time.RFC3339Nano, e.CreatedAt); err != nil {
		add("created_at", "FORMAT", "must be a real date-time")
	}
	if !contains(States, e.State) {
		add("state", "ENUM", "must be one of "+strings.Join(States, ", "))
	}
	switch e.State {
	case "exported", "landed":
		if e.ExportedPR == nil {
			add("exported_pr", "REQUIRED", "required once state is "+e.State)
		} else if !prURLPattern.MatchString(*e.ExportedPR) {
			add("exported_pr", "PATTERN", "must be the full URL of a GitHub pull request")
		}
	default:
		if e.ExportedPR != nil {
			add("exported_pr", "TYPE", "must be null while state is "+e.State)
		}
	}
	if !contains(Provenances, e.Provenance) {
		add("provenance", "ENUM", "must be one of "+strings.Join(Provenances, ", "))
	}
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
