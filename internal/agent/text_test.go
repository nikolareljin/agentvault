package agent

import (
	"strings"
	"testing"
)

func TestTextProblem(t *testing.T) {
	for in, want := range map[string]string{
		"plain\r\n":             "",
		"\xef\xbb\xbfbom\n":     "",
		"\xff\xfeh\x00":         "UTF-16",
		"\xfe\xff\x00h":         "UTF-16",
		"caf\xe9\n":             "not UTF-8",
		"\x89PNG\r\n\x1a\n\xff": "not UTF-8",
	} {
		got := TextProblem([]byte(in))
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("TextProblem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSkillDirOf(t *testing.T) {
	for in, want := range map[string]string{
		".claude/skills/s/assets/logo.png": ".claude/skills/s",
		"pkg/.claude/skills/s/a/b/c.bin":   "pkg/.claude/skills/s",
		`.Claude\Skills\s\x.png`:           ".Claude/Skills/s",
		".claude/skills/s.png":             "",
		"assets/logo.png":                  "",
		"":                                 "",
	} {
		if got := SkillDirOf(in); got != want {
			t.Errorf("SkillDirOf(%q) = %q, want %q", in, got, want)
		}
	}
}
