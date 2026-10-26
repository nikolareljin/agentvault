package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestContextRows(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "Projects")
	repo := filepath.Join(work, "app")
	writeTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), strings.Repeat("u", 100))
	writeTestFile(t, filepath.Join(work, "CLAUDE.md"), strings.Repeat("w", directoryInstructionBudget+1))
	writeTestFile(t, filepath.Join(repo, ".git", "HEAD"), "ref")
	writeTestFile(t, filepath.Join(repo, "CLAUDE.md"), strings.Repeat("r", 10000))
	writeTestFile(t, filepath.Join(home, ".claude", "projects", claudeProjectSlug(repo), "memory", "MEMORY.md"),
		strings.Repeat("- line\n", 250))
	writeTestFile(t, filepath.Join(home, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: Review: a PR\n---\nbody")
	plug := filepath.Join(home, "cache", "p")
	writeTestFile(t, filepath.Join(plug, "agents", "a.md"), "---\nname: a\ndescription: an agent\n---\n")
	writeTestFile(t, filepath.Join(home, "cache", "off", "agents", "b.md"), "---\nname: b\ndescription: off\n---\n")
	writeTestFile(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"p@m":true,"off@m":false}}`)
	writeTestFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"plugins":{"p@m":[{"installPath":"`+filepath.ToSlash(plug)+`"}],"off@m":[{"installPath":"`+filepath.ToSlash(filepath.Join(home, "cache", "off"))+`"}]}}`)

	rows := contextRows(home, repo)
	find := func(scope, suffix string) contextRow {
		t.Helper()
		for _, r := range rows {
			if r.Scope == scope && strings.HasSuffix(r.Source, suffix) {
				return r
			}
		}
		t.Fatalf("no %s row ending %q in %+v", scope, suffix, rows)
		return contextRow{}
	}
	if r := find("user", filepath.Join(".claude", "CLAUDE.md")); r.Bytes != 100 || r.Tokens != 25 || r.Over {
		t.Errorf("user row = %+v", r)
	}
	if r := find("directory", filepath.Join("Projects", "CLAUDE.md")); !r.Over || r.Budget != directoryInstructionBudget {
		t.Errorf("work dir row = %+v, want over budget", r)
	}
	if r := find("repository", filepath.Join("app", "CLAUDE.md")); r.Over || r.Budget != 0 {
		t.Errorf("repository row = %+v, want no budget", r)
	}
	if r := find("memory", "MEMORY.md"); r.Lines != 250 || !r.Over || r.Bytes != 200*len("- line\n") {
		t.Errorf("memory row = %+v, want 250 lines, over, 200 lines loaded", r)
	}
	if r := find("user", "skills listing (1)"); r.Bytes != len("review")+len("Review: a PR")+5 {
		t.Errorf("skills row = %+v", r)
	}
	find("plugin p@m", "agents listing (1)")
	for _, r := range rows {
		if strings.Contains(r.Scope, "off@m") {
			t.Errorf("disabled plugin listed: %+v", r)
		}
	}
	find("hooks", "SessionStart hook output")
}

func TestClaudeProjectSlug(t *testing.T) {
	if got := claudeProjectSlug("/home/me/Projects/my.app"); got != "-home-me-Projects-my-app" {
		t.Fatalf("slug = %q", got)
	}
}

func TestDoctorRequiresContextAndFailsOverBudget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), strings.Repeat("x", userInstructionBudget+1))
	dir := t.TempDir()
	for _, tc := range []struct {
		args    []string
		wantErr string
	}{
		{[]string{"--dir", dir}, "nothing to check"},
		{[]string{"--context", "--dir", dir}, "over budget"},
	} {
		doctorCmd.Flags().Set("context", "false")
		if err := doctorCmd.ParseFlags(tc.args); err != nil {
			t.Fatal(err)
		}
		err := runDoctor(doctorCmd, nil)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.wantErr)
		}
	}
	writeTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "small")
	doctorCmd.Flags().Set("context", "true")
	if err := runDoctor(doctorCmd, nil); err != nil {
		t.Errorf("within budget: err = %v", err)
	}
}
