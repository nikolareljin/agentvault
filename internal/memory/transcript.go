package memory

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Record is one memory.transcript.record@1 row: one line of an agent
// transcript as an importer stores it. Text may hold secrets and personal
// data; it is read by search over the loopback API only and never leaves
// the machine.
type Record struct {
	SchemaVersion string  `json:"schema_version"`
	SessionID     string  `json:"session_id"`
	Agent         string  `json:"agent"`
	TS            string  `json:"ts"`
	Role          string  `json:"role"`
	Text          *string `json:"text,omitempty"`
	Source        Source  `json:"source"`
}

// Source is where a transcript line was read from.
type Source struct {
	Path   string `json:"path"`
	Offset *int64 `json:"offset,omitempty"`
}

// Roles is the contract's role enum.
var Roles = []string{"user", "assistant", "system", "tool"}

var absolutePathPattern = regexp.MustCompile(`^/`)

// Validate returns every memory.transcript.record@1 violation of r, sorted
// by field, or nil.
func (r Record) Validate() []FieldError {
	var errs []FieldError
	add := func(field, code, detail string) {
		errs = append(errs, FieldError{Field: field, Code: code, Detail: detail})
	}
	if r.SchemaVersion != SchemaVersion {
		add("schema_version", "CONST", "must be \""+SchemaVersion+"\"")
	}
	if r.SessionID == "" {
		add("session_id", "MIN_LENGTH", "must not be empty")
	}
	if !agentPattern.MatchString(r.Agent) {
		add("agent", "PATTERN", "must match ^[a-z][a-z0-9-]{0,31}$")
	}
	if !timestampPattern.MatchString(r.TS) {
		add("ts", "PATTERN", "must be a UTC date-time, for example 2026-01-02T03:04:05Z")
	} else if _, err := time.Parse(time.RFC3339Nano, r.TS); err != nil {
		add("ts", "FORMAT", "must be a real date-time")
	}
	if !contains(Roles, r.Role) {
		add("role", "ENUM", "must be one of "+strings.Join(Roles, ", "))
	}
	if !absolutePathPattern.MatchString(r.Source.Path) {
		add("source.path", "PATTERN", "must be an absolute path")
	}
	if r.Source.Offset != nil && *r.Source.Offset < 0 {
		add("source.offset", "MINIMUM", "must be 0 or more")
	}
	if r.Text != nil && r.Source.Offset == nil {
		add("source.offset", "REQUIRED", "required when the record has text")
	}
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}
