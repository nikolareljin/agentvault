package agent

import (
	"strings"
	"testing"
	"time"
)

func TestCanonicalizeSourceProjectsFoldsCollisions(t *testing.T) {
	canon := func(s string) string { return strings.Replace(s, "/_git/", "/", 1) }
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	insts := []InstructionFile{
		{Name: "notes/a.md", Filename: "notes/a.md", Content: "old", UpdatedAt: t0, SourceProject: "remote:h/o/p/_git/r"},
		{Name: "AGENTS.md", Filename: "AGENTS.md", Content: "inst"},
		{Name: "notes/a.md", Filename: "notes/a.md", Content: "new", UpdatedAt: t0.Add(time.Hour), SourceProject: "remote:h/o/p/r"},
		{Name: "notes/b.md", Filename: "notes/b.md", Content: "b", SourceProject: "remote:h/o/p/_git/r"},
		// Unrelated duplicates nothing rewrote are left alone.
		{Name: "x", Filename: "x", Content: "1", SourceProject: "remote:g/o/r"},
		{Name: "x", Filename: "x", Content: "2", SourceProject: "remote:g/o/r"},
	}
	got := CanonicalizeSourceProjects(insts, canon)
	var names []string
	for _, i := range got {
		names = append(names, i.Name+"="+i.Content+"@"+i.SourceProject)
	}
	want := []string{
		"notes/a.md=new@remote:h/o/p/r",
		"AGENTS.md=inst@",
		"notes/b.md=b@remote:h/o/p/r",
		"x=1@remote:g/o/r",
		"x=2@remote:g/o/r",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("got\n %q\nwant\n %q", names, want)
	}
	if insts[0].SourceProject != "remote:h/o/p/_git/r" || insts[2].Content != "new" {
		t.Error("input slice was modified")
	}

	// Newer entry first: it is kept.
	got = CanonicalizeSourceProjects([]InstructionFile{insts[2], insts[0]}, canon)
	if len(got) != 1 || got[0].Content != "new" {
		t.Errorf("newer first: got %+v", got)
	}
}
