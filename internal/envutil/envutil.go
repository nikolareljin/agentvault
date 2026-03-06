package envutil

import (
	"runtime"
	"strings"
)

// SetValueWithPrecedence returns environment entries where key is removed from
// baseEnv and then re-appended with value (if value is non-empty).
func SetValueWithPrecedence(baseEnv []string, key string, value string) []string {
	out := make([]string, 0, len(baseEnv)+1)
	for _, entry := range baseEnv {
		entryKey, _, hasEquals := strings.Cut(entry, "=")
		if !hasEquals {
			out = append(out, entry)
			continue
		}
		if envKeyEquals(entryKey, key) {
			continue
		}
		out = append(out, entry)
	}
	if value != "" {
		out = append(out, key+"="+value)
	}
	return out
}

func envKeyEquals(left, right string) bool {
	return envKeyEqualsForOS(runtime.GOOS, left, right)
}

func envKeyEqualsForOS(goos, left, right string) bool {
	if goos == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
