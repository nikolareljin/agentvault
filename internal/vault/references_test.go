package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// The acceptance for this feature: an AGENTS.md naming three sibling files
// stores four, and a push recreates all four where they were.
func TestClosureStoresWhatTheInstructionsName(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	write(t, src, "AGENTS.md", "# Rules\n\nUse `implement_pr.txt` and `implement_issue.txt`.\n"+
		"The helper is `scripts/hygiene.sh`.\n")
	write(t, src, "implement_pr.txt", "pr template\n")
	write(t, src, "implement_issue.txt", "issue template\n")
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, src, filepath.Join("scripts", "hygiene.sh"), "#!/usr/bin/env bash\n")

	v := New(tempVaultPath(t))
	_ = v.Init("master")
	if err := v.SetInstruction(agent.InstructionFile{
		Name: "agents", Filename: "AGENTS.md",
		Content: mustRead(t, filepath.Join(src, "AGENTS.md")),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := v.PullReferenceClosure(src)
	if err != nil {
		t.Fatalf("PullReferenceClosure: %v", err)
	}
	if len(res.Stored) != 3 {
		t.Fatalf("stored %d referenced file(s), want 3: %+v", len(res.Stored), res.Stored)
	}
	if len(v.ListInstructions()) != 4 {
		t.Fatalf("vault holds %d instruction(s), want 4", len(v.ListInstructions()))
	}

	// push puts every one of them back, with the subdirectory intact.
	for _, inst := range v.ListInstructions() {
		p := filepath.Join(dst, inst.Filename)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(inst.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"AGENTS.md", "implement_pr.txt", "implement_issue.txt",
		filepath.Join("scripts", "hygiene.sh")} {
		if got := mustRead(t, filepath.Join(dst, rel)); got != mustRead(t, filepath.Join(src, rel)) {
			t.Errorf("%s did not round-trip", rel)
		}
	}
}

// Documentation naming itself, or two files naming each other, must terminate.
func TestClosureTerminatesOnACycle(t *testing.T) {
	src := t.TempDir()
	write(t, src, "AGENTS.md", "see `b.md`\n")
	write(t, src, "b.md", "back to `AGENTS.md`, and also `c.md`\n")
	write(t, src, "c.md", "and round to `b.md`\n")

	v := New(tempVaultPath(t))
	_ = v.Init("master")
	if err := v.SetInstruction(agent.InstructionFile{
		Name: "agents", Filename: "AGENTS.md",
		Content: mustRead(t, filepath.Join(src, "AGENTS.md")),
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var res ClosureResult
	var err error
	go func() {
		res, err = v.PullReferenceClosure(src)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the walk did not terminate on a cycle")
	}
	if err != nil {
		t.Fatalf("PullReferenceClosure: %v", err)
	}
	if len(res.Stored) != 2 {
		t.Fatalf("stored %d, want 2 (b.md and c.md, each once)", len(res.Stored))
	}
}

// A reference is relative to the file that names it, not to the pulled root.
// Resolving everything against the root reported a sibling as missing and
// refused a legitimate ../ back to the root file.
func TestClosureResolvesRelativeToTheReferencingFile(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, src, "AGENTS.md", "see `scripts/hygiene.sh`\n")
	write(t, src, filepath.Join("scripts", "hygiene.sh"),
		"sources `helpers.sh` and points back at `../AGENTS.md`\n")
	write(t, src, filepath.Join("scripts", "helpers.sh"), "helper\n")

	v := New(tempVaultPath(t))
	_ = v.Init("master")
	if err := v.SetInstruction(agent.InstructionFile{
		Name: "agents", Filename: "AGENTS.md",
		Content: mustRead(t, filepath.Join(src, "AGENTS.md")),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := v.PullReferenceClosure(src)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]bool{}
	for _, s := range res.Stored {
		stored[filepath.ToSlash(s.Filename)] = true
	}
	if !stored["scripts/helpers.sh"] {
		t.Errorf("a sibling named from scripts/ was not stored; stored=%v missing=%v",
			stored, res.Missing)
	}
	for _, r := range res.Refused {
		if r.Raw == "../AGENTS.md" {
			t.Errorf("../AGENTS.md refused as %q, but it is inside the directory", r.Reason)
		}
	}
}

// A refusal and a missing file are reported, not dropped.
func TestClosureReportsWhatItWillNotTake(t *testing.T) {
	src := t.TempDir()
	write(t, src, "AGENTS.md", "`/etc/passwd` `../up.txt` `gone.txt`\n")

	v := New(tempVaultPath(t))
	_ = v.Init("master")
	if err := v.SetInstruction(agent.InstructionFile{
		Name: "agents", Filename: "AGENTS.md",
		Content: mustRead(t, filepath.Join(src, "AGENTS.md")),
	}); err != nil {
		t.Fatal(err)
	}

	res, err := v.PullReferenceClosure(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stored) != 0 {
		t.Errorf("stored %d file(s), want none", len(res.Stored))
	}
	if len(res.Refused) != 2 {
		t.Errorf("refused %d, want 2 (absolute and ..): %+v", len(res.Refused), res.Refused)
	}
	if len(res.Missing) != 1 {
		t.Errorf("missing %d, want 1 (gone.txt): %+v", len(res.Missing), res.Missing)
	}
	for _, r := range append(res.Refused, res.Missing...) {
		if r.Reason == "" {
			t.Errorf("%s came back without a reason", r.Raw)
		}
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
