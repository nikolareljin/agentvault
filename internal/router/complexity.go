package router

import (
	"regexp"
	"strings"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// Complexity is the result of ScoreComplexity: the tier and the points
// behind it.
type Complexity struct {
	Tier    string   `json:"tier"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
}

// Complexity thresholds. Points add up as follows:
//
//	words      >= 40: 1,  >= 150: 2,  >= 400: 3
//	steps      numbered or bulleted lines: >= 3: 1,  >= 6: 2
//	code/tool  a ``` fence: 1; coding, review or analysis intent (the
//	           router's classifyPrompt): 1
//	multi-step sequencing words (then, next, after that, finally, and
//	           then, step): >= 2: 1
//
// A total of 0-1 is simple, 2-3 standard, 4 or more deep.
const (
	complexityWordsLow      = 40
	complexityWordsMid      = 150
	complexityWordsHigh     = 400
	complexityStepsLow      = 3
	complexityStepsHigh     = 6
	complexitySequenceMin   = 2
	complexityStandardScore = 2
	complexityDeepScore     = 4
)

var (
	stepLineRE     = regexp.MustCompile(`^\s*(\d+[.)]|[-*+])\s+\S`)
	sequenceWordRE = regexp.MustCompile(`\b(then|next|after that|afterwards|finally|step)\b`)
)

// ScoreComplexity maps a prompt to simple, standard or deep from its
// length, instruction depth, code and tool indicators and multi-step
// signals. It is a pure, deterministic function; see the constants above
// for the thresholds.
func ScoreComplexity(prompt string) Complexity {
	var c Complexity
	add := func(n int, reason string) {
		if n > 0 {
			c.Score += n
			c.Reasons = append(c.Reasons, reason)
		}
	}

	words := len(strings.Fields(prompt))
	switch {
	case words >= complexityWordsHigh:
		add(3, "words>=400")
	case words >= complexityWordsMid:
		add(2, "words>=150")
	case words >= complexityWordsLow:
		add(1, "words>=40")
	}

	steps := 0
	for _, line := range strings.Split(prompt, "\n") {
		if stepLineRE.MatchString(line) {
			steps++
		}
	}
	switch {
	case steps >= complexityStepsHigh:
		add(2, "steps>=6")
	case steps >= complexityStepsLow:
		add(1, "steps>=3")
	}

	if strings.Contains(prompt, "```") {
		add(1, "code-block")
	}
	intent := classifyPrompt(prompt)
	if intent.TaskClass == "coding" || intent.TaskClass == "review" || intent.TaskClass == "analysis" {
		add(1, "intent="+intent.TaskClass)
	}

	if n := len(sequenceWordRE.FindAllString(strings.ToLower(prompt), -1)); n >= complexitySequenceMin {
		add(1, "multi-step")
	}

	switch {
	case c.Score >= complexityDeepScore:
		c.Tier = agent.VariantTierDeep
	case c.Score >= complexityStandardScore:
		c.Tier = agent.VariantTierStandard
	default:
		c.Tier = agent.VariantTierSimple
	}
	return c
}
