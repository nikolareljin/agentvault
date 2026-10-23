package router

import (
	"strings"
	"testing"
)

func words(n int) string {
	return strings.TrimSpace(strings.Repeat("word ", n))
}

func steps(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("\n")
		b.WriteString(string(rune('0'+i%10)) + ". item")
	}
	return b.String()
}

func TestScoreComplexityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prompt string
		score  int
		tier   string
	}{
		{"empty", "", 0, "simple"},
		{"short", "what time is it", 0, "simple"},
		{"39 words", words(39), 0, "simple"},
		{"40 words", words(40), 1, "simple"},
		{"149 words", words(149), 1, "simple"},
		{"150 words", words(150), 2, "standard"},
		{"399 words", words(399), 2, "standard"},
		{"400 words", words(400), 3, "standard"},
		{"2 steps", "do it" + steps(2), 0, "simple"},
		{"3 steps", "do it" + steps(3), 1, "simple"},
		{"5 bullets", "do it\n- a\n- b\n* c\n+ d\n- e", 1, "simple"},
		{"6 steps", "do it" + steps(6), 2, "standard"},
		{"fence", "look\n```\nx := 1\n```", 1, "simple"},
		{"coding intent", "refactor this function", 1, "simple"},
		{"one sequence word", "first read, then answer", 0, "simple"},
		{"two sequence words", "read it, then think, finally reply", 1, "simple"},
		{"400 words and 6 steps", words(400) + steps(6), 5, "deep"},
		{"150 words, 3 steps, multi-step", words(150) + steps(3) + "\nthen x then y", 4, "deep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ScoreComplexity(tc.prompt)
			if got.Score != tc.score || got.Tier != tc.tier {
				t.Fatalf("ScoreComplexity = %d %s (%v), want %d %s", got.Score, got.Tier, got.Reasons, tc.score, tc.tier)
			}
			if again := ScoreComplexity(tc.prompt); again.Score != got.Score || again.Tier != got.Tier {
				t.Fatal("not deterministic")
			}
		})
	}
}
