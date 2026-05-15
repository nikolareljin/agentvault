package cmd

import (
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestValidateScopeFlagsFormatsScopeErrorsForCLI(t *testing.T) {
	cases := []struct {
		name    string
		scope   string
		pattern string
		want    string
	}{
		{
			name:  "invalid scope",
			scope: "bad",
			want:  `invalid --scope "bad"; valid: global, directory, local`,
		},
		{
			name:    "pattern requires directory scope",
			scope:   agent.InstructionScopeLocal,
			pattern: "/repo",
			want:    "--directory-pattern is only valid for directory scope",
		},
		{
			name:  "directory scope requires pattern",
			scope: agent.InstructionScopeDirectory,
			want:  "--directory-pattern is required for directory scope",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateScopeFlags(tc.scope, tc.pattern)
			if err == nil {
				t.Fatal("validateScopeFlags() error = nil")
			}
			if err.Error() != tc.want {
				t.Fatalf("validateScopeFlags() error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}
