package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestParseTUIInvocation_DefaultNoArgs(t *testing.T) {
	launch, target, err := parseTUIInvocation(nil)
	if err != nil {
		t.Fatalf("parseTUIInvocation(nil) error = %v", err)
	}
	if !launch {
		t.Fatalf("launch = false, want true")
	}
	if target != "agents" {
		t.Fatalf("target = %q, want agents", target)
	}
}

func TestParseTUIInvocation_NoTUIFlagWithCommand(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"list"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(list) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false (target=%q)", target)
	}
}

func TestParseTUIInvocation_FlagOnly(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(-t) error = %v", err)
	}
	if !launch || target != "agents" {
		t.Fatalf("launch,target = %v,%q want true,agents", launch, target)
	}
}

func TestParseTUIInvocation_ExplicitTarget(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"--tui", "commands"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(--tui commands) error = %v", err)
	}
	if !launch || target != "commands" {
		t.Fatalf("launch,target = %v,%q want true,commands", launch, target)
	}
}

func TestParseTUIInvocation_InlineShorthandRules(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"-trules"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(-trules) error = %v", err)
	}
	if !launch || target != "rules" {
		t.Fatalf("launch,target = %v,%q want true,rules", launch, target)
	}
}

func TestParseTUIInvocation_InlineShorthandAgents(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"-tagents"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(-tagents) error = %v", err)
	}
	if !launch || target != "agents" {
		t.Fatalf("launch,target = %v,%q want true,agents", launch, target)
	}
}

func TestParseTUIInvocation_InferTargetFromCommand(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"detect", "add", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(detect add -t) error = %v", err)
	}
	if !launch || target != "detected" {
		t.Fatalf("launch,target = %v,%q want true,detected", launch, target)
	}
}

func TestParseTUIInvocation_InferTargetWithConfigFlag(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"--config", "/tmp/agentvault", "detect", "add", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(--config ... detect add -t) error = %v", err)
	}
	if !launch || target != "detected" {
		t.Fatalf("launch,target = %v,%q want true,detected", launch, target)
	}
}

func TestParseTUIInvocation_InferTargetFromInstAlias(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"inst", "list", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(inst list -t) error = %v", err)
	}
	if !launch || target != "instructions" {
		t.Fatalf("launch,target = %v,%q want true,instructions", launch, target)
	}
}

func TestParseTUIInvocation_InferTargetFromSessAlias(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"sess", "list", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(sess list -t) error = %v", err)
	}
	if !launch || target != "sessions" {
		t.Fatalf("launch,target = %v,%q want true,sessions", launch, target)
	}
}

func TestParseTUIInvocation_InvalidTarget(t *testing.T) {
	_, _, err := parseTUIInvocation([]string{"--tui", "invalid-target"})
	if err == nil {
		t.Fatalf("expected invalid target error")
	}
}

func TestParseTUIInvocation_LastBareTUIFlagClearsEarlierAssignedTarget(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"--tui=commands", "detect", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(--tui=commands detect -t) error = %v", err)
	}
	if !launch || target != "detected" {
		t.Fatalf("launch,target = %v,%q want true,detected", launch, target)
	}
}

func TestParseTUIInvocation_ExplicitEmptyAssignmentDoesNotConsumeNextToken(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"--tui=", "commands"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(--tui= commands) error = %v", err)
	}
	if !launch || target != "commands" {
		t.Fatalf("launch,target = %v,%q want true,commands", launch, target)
	}
}

func TestParseTUIInvocation_IgnoresTUIFlagsAfterDoubleDash(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"detect", "--", "-t", "rules"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(detect -- -t rules) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false (target=%q)", target)
	}
}

func TestApplyEarlyPersistentFlags_StopsAtDoubleDash(t *testing.T) {
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("config", "")
	})
	if err := applyEarlyPersistentFlags([]string{"detect", "--", "--config"}); err != nil {
		t.Fatalf("applyEarlyPersistentFlags should ignore --config after --, got error: %v", err)
	}
	got, _ := rootCmd.PersistentFlags().GetString("config")
	if got != "" {
		t.Fatalf("config flag = %q, want empty", got)
	}
}

func TestFirstCommandToken_UsesPositionalAfterDoubleDash(t *testing.T) {
	token, ok := firstCommandToken([]string{"--config", "/tmp/custom", "--", "-t"})
	if !ok {
		t.Fatalf("expected token after --")
	}
	if token != "-t" {
		t.Fatalf("token = %q, want -t", token)
	}
}

func TestParseTUIInvocation_EarlierBareTUIValueIsNotCommandToken(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"--tui", "commands", "detect", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(--tui commands detect -t) error = %v", err)
	}
	if !launch || target != "detected" {
		t.Fatalf("launch,target = %v,%q want true,detected", launch, target)
	}
}

func TestApplyEarlyPersistentFlags_ConfigWithSeparateValue(t *testing.T) {
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("config", "")
	})
	if err := applyEarlyPersistentFlags([]string{"--config", "/tmp/custom"}); err != nil {
		t.Fatalf("applyEarlyPersistentFlags(--config /tmp/custom) error = %v", err)
	}
	got, _ := rootCmd.PersistentFlags().GetString("config")
	if got != "/tmp/custom" {
		t.Fatalf("config flag = %q, want /tmp/custom", got)
	}
}

func TestApplyEarlyPersistentFlags_ConfigWithEqualsValue(t *testing.T) {
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("config", "")
	})
	if err := applyEarlyPersistentFlags([]string{"--config=/tmp/alt"}); err != nil {
		t.Fatalf("applyEarlyPersistentFlags(--config=/tmp/alt) error = %v", err)
	}
	got, _ := rootCmd.PersistentFlags().GetString("config")
	if got != "/tmp/alt" {
		t.Fatalf("config flag = %q, want /tmp/alt", got)
	}
}

func TestApplyEarlyPersistentFlags_ConfigMissingValue(t *testing.T) {
	err := applyEarlyPersistentFlags([]string{"--config", "--tui"})
	if err == nil {
		t.Fatalf("expected missing config value error")
	}
}

func TestContainsHelpFlag(t *testing.T) {
	if !containsHelpFlag([]string{"--help"}) {
		t.Fatalf("expected containsHelpFlag to detect --help")
	}
	if !containsHelpFlag([]string{"list", "-h"}) {
		t.Fatalf("expected containsHelpFlag to detect -h")
	}
	if containsHelpFlag([]string{"list"}) {
		t.Fatalf("did not expect containsHelpFlag to detect help")
	}
	if containsHelpFlag([]string{"detect", "--", "--help"}) {
		t.Fatalf("did not expect containsHelpFlag to detect help after --")
	}
}

func TestContainsHelpFlag_StopsAtDoubleDash(t *testing.T) {
	if containsHelpFlag([]string{"detect", "--", "--help"}) {
		t.Fatalf("did not expect help detection for --help after --")
	}
}

func TestExecute_HelpBypassesTUILaunch(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	os.Args = []string{"agentvault", "--tui", "--help"}

	err := Execute()
	if err != nil {
		t.Fatalf("Execute() with --help returned error: %v", err)
	}
}

func TestExecute_DoesNotReuseStaleSetArgsAfterHelp(t *testing.T) {
	origArgs := os.Args
	origOut := rootCmd.OutOrStdout()
	origErr := rootCmd.ErrOrStderr()
	t.Cleanup(func() {
		os.Args = origArgs
		rootCmd.SetOut(origOut)
		rootCmd.SetErr(origErr)
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)

	os.Args = []string{"agentvault", "--tui", "--help"}
	if err := Execute(); err != nil {
		t.Fatalf("Execute() with help returned error: %v", err)
	}

	os.Args = []string{"agentvault", "__definitely_invalid_command__"}
	err := Execute()
	if err == nil {
		t.Fatalf("expected invalid command error after help invocation")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command error, got: %v", err)
	}
}

func TestIsVaultNotFoundError(t *testing.T) {
	if !isVaultNotFoundError(os.ErrNotExist) {
		t.Fatalf("os.ErrNotExist should be treated as vault-not-found fallback")
	}
	if !isVaultNotFoundError(assertErr("vault not found at /tmp/vault.enc")) {
		t.Fatalf("vault not found message should be treated as fallback")
	}
	if isVaultNotFoundError(assertErr("invalid password")) {
		t.Fatalf("non not-found errors must not fallback")
	}
}

func assertErr(msg string) error {
	return &testErr{msg: msg}
}

type testErr struct {
	msg string
}

func (e *testErr) Error() string {
	return e.msg
}
