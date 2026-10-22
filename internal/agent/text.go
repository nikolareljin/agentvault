package agent

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// TextProblem is why data cannot be stored as an instruction, or "" when it
// can. The vault stores instructions as UTF-8 text: anything else would be
// saved with its invalid bytes replaced and pushed back changed.
func TextProblem(data []byte) string {
	if utf8.Valid(data) {
		return ""
	}
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		return "UTF-16 text; save it as UTF-8 to store it"
	}
	return "not UTF-8 text (a binary file or another encoding); the vault stores UTF-8 text only"
}

// SkillDirOf is the skill directory (".claude/skills/NAME", with any project
// prefix) that rel sits in, as rel spells it, or "".
func SkillDirOf(rel string) string {
	raw := strings.Split(strings.ReplaceAll(rel, "\\", "/"), "/")
	segs := strings.Split(NormalizePathForMatch(rel), "/")
	for i := 0; i+3 < len(segs); i++ {
		if segs[i] == ".claude" && segs[i+1] == "skills" {
			return strings.Join(raw[:i+3], "/")
		}
	}
	return ""
}
