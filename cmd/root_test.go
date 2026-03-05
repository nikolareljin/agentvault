package cmd

import (
	"bytes"
	"fmt"
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

func TestParseTUIInvocation_BareFlagThenCommandInfersTarget(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"-t", "detect", "add"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(-t detect add) error = %v", err)
	}
	if !launch || target != "detected" {
		t.Fatalf("launch,target = %v,%q want true,detected", launch, target)
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
	if !strings.Contains(err.Error(), "invalid TUI target") {
		t.Fatalf("expected TUI target wording, got: %v", err)
	}
}

func TestParseTUIInvocation_ExplicitAliasIsRejected(t *testing.T) {
	_, _, err := parseTUIInvocation([]string{"--tui", "add"})
	if err == nil {
		t.Fatalf("expected explicit alias target to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid TUI target") {
		t.Fatalf("expected TUI target wording, got: %v", err)
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

func TestParsePromptModeInvocation_FlagOnly(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"-p"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(-p) error = %v", err)
	}
	if !launch {
		t.Fatalf("launch = false, want true")
	}
}

func TestParsePromptModeInvocation_LongFlagOnly(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"--prompt-mode"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(--prompt-mode) error = %v", err)
	}
	if !launch {
		t.Fatalf("launch = false, want true")
	}
}

func TestParsePromptModeInvocation_LongFlagEqualsTrue(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"--prompt-mode=true"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(--prompt-mode=true) error = %v", err)
	}
	if !launch {
		t.Fatalf("launch = false, want true")
	}
}

func TestParsePromptModeInvocation_ShortFlagEqualsTrue(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"-p=true"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(-p=true) error = %v", err)
	}
	if !launch {
		t.Fatalf("launch = false, want true")
	}
}

func TestParsePromptModeInvocation_LongFlagEqualsFalseDoesNotLaunch(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"--prompt-mode=false"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(--prompt-mode=false) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false")
	}
}

func TestParsePromptModeInvocation_LongFlagSpaceFalseDoesNotLaunch(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"--prompt-mode", "false"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(--prompt-mode false) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false")
	}
}

func TestParsePromptModeInvocation_ShortFlagEqualsFalseDoesNotLaunch(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"-p=false"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(-p=false) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false")
	}
}

func TestParsePromptModeInvocation_ShortFlagSpaceFalseDoesNotLaunch(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"-p", "false"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(-p false) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false")
	}
}

func TestParsePromptModeInvocation_WithCommandDoesNotIntercept(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"detect", "-p"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(detect -p) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false")
	}
}

func TestParsePromptModeInvocation_RootFlagThenCommandReturnsActionableError(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"-p", "detect"})
	if err == nil {
		t.Fatalf("expected prompt mode + command error")
	}
	if launch {
		t.Fatalf("launch = true, want false")
	}
	if !strings.Contains(err.Error(), "prompt mode flag must be used without a command") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePromptModeInvocation_FlagAfterDoubleDashIgnored(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"detect", "--", "-p"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(detect -- -p) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false")
	}
}

func TestParsePromptModeInvocation_UnknownFlagErrors(t *testing.T) {
	_, err := parsePromptModeInvocation([]string{"-p", "--bogus"})
	if err == nil {
		t.Fatalf("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag for prompt mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePromptModeInvocation_TUIFlagReturnsMutualExclusionError(t *testing.T) {
	_, err := parsePromptModeInvocation([]string{"-p", "--tui"})
	if err == nil {
		t.Fatalf("expected prompt mode and tui mutual exclusion error")
	}
	if !strings.Contains(err.Error(), "prompt mode cannot be combined with --tui/-t") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePromptModeInvocation_InvalidBooleanValueErrors(t *testing.T) {
	_, err := parsePromptModeInvocation([]string{"--prompt-mode=maybe"})
	if err == nil {
		t.Fatalf("expected invalid boolean error")
	}
	if !strings.Contains(err.Error(), "invalid boolean value for --prompt-mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePromptModeInvocation_InvalidShortBooleanValueErrors(t *testing.T) {
	_, err := parsePromptModeInvocation([]string{"-p=maybe"})
	if err == nil {
		t.Fatalf("expected invalid boolean error")
	}
	if !strings.Contains(err.Error(), "invalid boolean value for -p") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePromptModeInvocation_AllowsConfigFlag(t *testing.T) {
	launch, err := parsePromptModeInvocation([]string{"-p", "--config", "/tmp/agentvault"})
	if err != nil {
		t.Fatalf("parsePromptModeInvocation(-p --config ...) error = %v", err)
	}
	if !launch {
		t.Fatalf("launch = false, want true")
	}
}

func TestParsePromptModeInvocation_DoesNotInterceptSubcommandProviderFlag(t *testing.T) {
	for _, args := range [][]string{
		{"add", "-p", "claude"},
		{"edit", "my-agent", "-p", "codex"},
	} {
		launch, err := parsePromptModeInvocation(args)
		if err != nil {
			t.Fatalf("parsePromptModeInvocation(%v) error = %v", args, err)
		}
		if launch {
			t.Fatalf("parsePromptModeInvocation(%v) launch = true, want false", args)
		}
	}
}

func TestStripPromptModeFlags(t *testing.T) {
	got := stripPromptModeFlags([]string{"-p", "--config", "/tmp/cfg", "--prompt-mode", "--help"})
	if strings.Join(got, " ") != "--config /tmp/cfg --help" {
		t.Fatalf("stripPromptModeFlags() = %q", got)
	}
}

func TestStripPromptModeFlags_PreservesArgsAfterDoubleDash(t *testing.T) {
	got := stripPromptModeFlags([]string{"detect", "--", "-p", "--prompt-mode"})
	if strings.Join(got, " ") != "detect -- -p --prompt-mode" {
		t.Fatalf("stripPromptModeFlags() after -- = %q", got)
	}
}

func TestStripPromptModeFlags_RemovesExplicitFalseToken(t *testing.T) {
	got := stripPromptModeFlags([]string{"--config", "/tmp/cfg", "--prompt-mode=false"})
	if strings.Join(got, " ") != "--config /tmp/cfg" {
		t.Fatalf("stripPromptModeFlags() = %q", got)
	}
}

func TestStripPromptModeFlags_RemovesShortExplicitFalseToken(t *testing.T) {
	got := stripPromptModeFlags([]string{"--config", "/tmp/cfg", "-p=false"})
	if strings.Join(got, " ") != "--config /tmp/cfg" {
		t.Fatalf("stripPromptModeFlags() = %q", got)
	}
}

func TestStripPromptModeFlags_RemovesSpaceSeparatedBooleanToken(t *testing.T) {
	got := stripPromptModeFlags([]string{"--config", "/tmp/cfg", "--prompt-mode", "false", "--help"})
	if strings.Join(got, " ") != "--config /tmp/cfg --help" {
		t.Fatalf("stripPromptModeFlags() = %q", got)
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

func TestExecute_CommandWithPromptModeFlagReturnsCobraUnknownFlag(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	os.Args = []string{"agentvault", "detect", "-p"}

	err := Execute()
	if err == nil {
		t.Fatalf("expected error for command + -p combination")
	}
	if !strings.Contains(err.Error(), "unknown shorthand flag") && !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsVaultNotFoundError(t *testing.T) {
	if !isVaultNotFoundError(os.ErrNotExist) {
		t.Fatalf("os.ErrNotExist should be treated as vault-not-found fallback")
	}
	if !isVaultNotFoundError(fmt.Errorf("wrapped: %w", ErrVaultNotFound)) {
		t.Fatalf("ErrVaultNotFound should be treated as vault-not-found fallback")
	}
	if isVaultNotFoundError(fmt.Errorf("invalid password")) {
		t.Fatalf("non not-found errors must not fallback")
	}
}
