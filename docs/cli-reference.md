# AgentVault CLI Reference

This file lists all CLI commands and flags.

Related docs: [Docs Index](./README.md) | [TUI Reference](./tui-reference.md) | [Workflows](./workflows.md) | [Detailed Usage](./USAGE_DETAILED.md)

## Global usage

```bash
agentvault [global flags] [command] [subcommand] [args] [flags]
```

## Global flags

- `--config <dir>`: custom config dir, default `~/.config/agentvault`
- `-t, --tui [target]`: launch TUI (also the default when no command is provided)
  - Supported targets: `agents`, `instructions`, `rules`, `sessions`, `detected`, `commands`, `status`, `about`
  - When `-t` is used with a command (example: `agentvault detect add -t`), AgentVault opens TUI on the inferred matching tab and does not run the command directly

## Top-level commands

- `init`
- `unlock`
- `version`
- `detect`
- `add`
- `list`
- `edit`
- `remove`
- `run`
- `prompt`
- `route`
- `status`
- `capability`
- `routing-model`
- `rules`
- `roles`
- `session`
- `sync`
- `instructions` (alias: `inst`)
- `config`
- `generate`
- `setup`
- `export`
- `import`

## Vault lifecycle

### `agentvault init`
No flags.

### `agentvault unlock`
No flags.

### `agentvault version`
No flags.

## Agent discovery and CRUD

### `agentvault detect`
Flags:
- `--json` (default: `false`)
- `--verbose` (default: `false`)

### `agentvault detect add`
Flags:
- `--force` (default: `false`)

### `agentvault add [name]`
Required:
- `-p, --provider <provider>`

Optional:
- `-m, --model <model>`
- `--backend <backend>`: `anthropic|ollama|bedrock`
- `-k, --api-key <key>`
- `--base-url <url>`
- `--system-prompt <text>`
- `--task-desc <text>`
- `--tags <comma-separated>`
- `--route-capabilities <comma-separated>`
- `--latency-tier <tier>`: `low|medium|high`
- `--cost-tier <tier>`: `low|medium|high`
- `--privacy-tier <tier>`: `local|restricted|remote`
- `--route-priority <int>`
- `--disable-routing` (default: `false`)

### `agentvault list`
Flags:
- `--json` (default: `false`)
- `--show-keys` (default: `false`)

### `agentvault edit [name]`
Flags:
- `-p, --provider <provider>`
- `-m, --model <model>`
- `--backend <backend>`: `anthropic|ollama|bedrock`
- `-k, --api-key <key>`
- `--base-url <url>`
- `--system-prompt <text>`
- `--task-desc <text>`
- `--tags <comma-separated>`
- `--role <role-name>`
- `--disable-rules <comma-separated-rule-names>`
- `--route-capabilities <comma-separated>`
- `--latency-tier <tier>`: `low|medium|high`
- `--cost-tier <tier>`: `low|medium|high`
- `--privacy-tier <tier>`: `local|restricted|remote`
- `--route-priority <int>`
- `--disable-routing` (default: `false`)

### `agentvault remove [name]`
Flags:
- `-f, --force` (default: `false`)

### `agentvault run [name]`
Flags:
- `--env` (default: `false`)

## Prompt and status

### `agentvault prompt [agent-name]`

Argument rules:
- default mode requires exactly one `agent-name`
- `--auto` requires no positional agent argument and lets AgentVault choose the target via the configured routing mode
Input:
- Without `--workflow` (default), one primary prompt source is required:
  - `--text <prompt>` or
  - `--file <path>` or
  - stdin
- With `--workflow`, the main task comes from workflow context and `--text`, `--file`, or stdin become optional operator notes.

Flags:
- `--text <prompt>`
- `--file <path>`
- `--auto` (default: `false`): automatic agent selection — no positional agent argument required
- `--workflow <name>`: `implement_issue|issue|implement_pr|pr|fix_pr`
- `--repo <path>`: repository path for workflow context (default: current directory)
- `--workspace <path>`: execution workspace for agentic CLI providers
- `--issue <ref>`: required with `--workflow implement_issue` or `issue`
- `--pr <ref>`: required with `--workflow implement_pr`, `pr`, or `fix_pr`
- `--json` (default: `false`)
- `--optimize` (default: `true`)
- `--optimize-profile <profile>` (default: `auto`): `auto|generic|ollama|codex|copilot|claude`
- `--optimize-ollama` (default: `true`, compatibility)
- `--dry-run` (default: `false`)
- `--validate-only` (default: `false`)
- `--no-log` (default: `false`)
- `--history-file <path>`
- `--timeout <duration>` (default: `5m`)

Routing flags (apply when `--auto` is used or router mode is set):
- `--router <mode>`: `heuristic|langgraph|local-ai|llm-router`
- `--importance <level>`: `low|medium|high|critical`
- `--deadline <level>`: `immediate|normal|background`
- `--prefer-local` (default: `false`)
- `--prefer-fast` (default: `false`)
- `--prefer-low-cost` (default: `false`)
- `--local-only` (default: `false`)
- `--local-ai-model <model>` (default: `llama3.2`)
- `--local-ai-url <url>` (default: `http://localhost:11434`)
- `--llm-router-url <url>`
- `--llm-router-model <name>`
- `--llm-router-timeout <int>` (default: `30`)
- `--llm-router-model-path <path>`: GGUF model for embedded inference (requires `make build-bitnet`)
- `--llm-router-context-size <int>` (default: `512`)
- `--llm-router-threads <int>` (default: all available)
- `--llm-router-gpu-layers <int>` (default: `0`)

Execution behavior:
- Codex launches in agentic workspace-write mode (`--full-auto`).
- Claude launches with `--permission-mode auto`.
- Gemini launches with `--approval-mode auto_edit`.
- Gemini receives both `GEMINI_API_KEY` and `GOOGLE_API_KEY` from AgentVault runtime config.
- Ollama and OpenAI runners use their HTTP execution paths.
- Prompt text does not implicitly switch repositories.
- `--repo` controls workflow metadata and prompt generation.
- `--workspace` controls actual subprocess working directory for Codex, Claude, and Gemini.
- If `--workspace` is omitted and workflow mode is active, AgentVault runs in resolved `--repo` git root.
- If both `--workspace` and `--repo` are omitted, AgentVault runs in current shell directory.

Workflow behavior:
- resolves the git repository root and current branch from `--repo`
- loads the canonical workflow template with precedence `repo-local -> config storage -> built-in`
- fetches issue or PR metadata with `gh`
- injects structured progress checkpoints (`Intake`, `Context`, `Implementation`, `Validation`, `Delivery`) into the generated prompt
- disables prompt optimization by default so the canonical workflow checkpoints remain unchanged; pass `--optimize=true` or set `--optimize-profile` explicitly to opt back in
- requires `git` and `gh` to be installed and available on `PATH`
- requires `gh` to be authenticated for the target repository or host
- uses `AGENTVAULT_PROMPT_WORKFLOW_TIMEOUT` when set; otherwise workflow `git`/`gh` subprocesses derive a bounded timeout from `--timeout`, clamped into the `30s` to `2m` range

### `agentvault status`
Flags:
- `--json` (default: `true`)
- `--no-vault` (default: `false`)
- `--vault-password-env <ENV_VAR>` (default: `AGENTVAULT_PASSWORD`)
- `--cost-report` (default: `false`): include estimated cost breakdown aggregated from prompt history; fires budget alerts when spend exceeds 80% of `MonthlyBudgetUSD`

### `agentvault route`
Input:
- one prompt source is required:
  - `--text <prompt>` or
  - `--file <path>` or
  - stdin

Flags:
- `--text <prompt>`
- `--file <path>`
- `--json` (default: `false`)
- `--router <mode>`: `heuristic|langgraph|local-ai|llm-router`
- `--langgraph-cmd <path-to-python-script>`
- `--prefer-local` (default flag value: `false`; effective default policy is local-first when no preference flags are set)
- `--prefer-fast` (default: `false`)
- `--prefer-low-cost` (default: `false`)
- `--local-only` (default: `false`)
- `--importance <level>`: `low|medium|high|critical` — influences scoring; `critical` strongly prefers cloud/low-latency runners
- `--deadline <level>`: `immediate|normal|background` — `immediate` boosts low-latency agents; `background` favors low-cost
- `--local-ai-model <model>` (default: `llama3.2`): Ollama model for `local-ai` routing classification
- `--local-ai-url <url>` (default: `http://localhost:11434`): Ollama base URL for `local-ai` routing
- `--llm-router-url <url>`: OpenAI-compatible server URL for `llm-router` mode (e.g. `http://localhost:8080`)
- `--llm-router-model <name>`: model name override for `llm-router` HTTP mode (uses server default if empty)
- `--llm-router-timeout <int>` (default: `30`): `llm-router` HTTP request timeout in seconds
- `--llm-router-model-path <path>`: path to GGUF model file for embedded in-process inference (requires `make build-bitnet`)
- `--llm-router-context-size <int>` (default: `512`): context window tokens for embedded inference
- `--llm-router-threads <int>` (default: all available): CPU threads for embedded inference
- `--llm-router-gpu-layers <int>` (default: `0`): transformer layers to offload to GPU for embedded inference

Behavior:
- inspects all configured agents, their routing metadata, and the vault capability registry
- selects the best agent/runner/model combination without executing the prompt
- defaults to a local-first routing policy when none of `--prefer-local`, `--prefer-fast`, or `--prefer-low-cost` are set
- `local-ai` mode sends prompt to a local Ollama model for structured classification, then scores agents; falls back to heuristic if Ollama is unreachable and `--allow-fallbacks` is set, otherwise returns an error
- `llm-router` mode calls an external OpenAI-compatible server OR an embedded GGUF engine (when `--llm-router-model-path` is set) for intelligent cost-aware routing; falls back to heuristic on error when `--allow-fallbacks` is set, otherwise returns an error
- returns fallback candidates when `--allow-fallbacks` is set
- output includes: Mode, Importance, Deadline, task class, top reasons, and fallback list

## Capability registry

### `agentvault capability list`
Flags:
- `--json` (default: `false`)

### `agentvault capability add`
Required:
- `--endpoint <url>`: endpoint base URL
- `--model <name>`: model name

Optional:
- `--context <int>`: context window size in tokens
- `--caps <comma-separated>`: capability tags — routing: `coding`, `review`, `analysis`, `general`; informational: `vision`, `embedding`

### `agentvault capability remove`
Required:
- `--endpoint <url>`
- `--model <name>`

### `agentvault capability discover`
Required:
- `--endpoint <url>`: base URL to query for model capabilities

Optional:
- `--timeout <duration>` (default: `10s`): request timeout

Behavior: tries `/v1/models` (OpenAI-compat shape — llama.cpp, Ollama, bitnet-server) first,
then falls back to `/health` (models-list shape). Infers capability tags from model names.
Registry entries are used by all routing modes to augment agent `RouteConfig.Capabilities`.

## Embedded inference model

### `agentvault routing-model status`
No flags. Prints engine state (enabled/disabled based on build tag), expected model path,
file size, and OS/arch. The embedded engine requires `make build-bitnet`.

### `agentvault routing-model download`
Flags:
- `--output <dir>` (default: `~/.local/share/agentvault/models/`): directory to save the GGUF file
- `--url <url>` (default: HuggingFace BitNet-b1.58-2B-4T URL): download URL override

Downloads `bitnet_b1_58-2B-4T.gguf` (~400 MB) from Hugging Face with streaming progress.
Re-running is a no-op if the file already exists.

## Rules (`rules` alias: `rule`)

### `agentvault rules list`
Flags:
- `--all` (default: `false`)
- `--json` (default: `false`)

### `agentvault rules show [name]`
No flags.

### `agentvault rules add [name]`
Flags:
- `--description <text>`
- `--content <text>`
- `--category <name>` (default: `general`)
- `--priority <int>` (default: `50`)

### `agentvault rules edit [name]`
Flags:
- `--description <text>`
- `--content <text>`
- `--category <name>`
- `--priority <int>`
- `--enabled <bool>` (default: `true`)

### `agentvault rules remove [name]`
No flags.

### `agentvault rules enable [name]`
No flags.

### `agentvault rules disable [name]`
No flags.

### `agentvault rules init`
Flags:
- `--force` (default: `false`)

### `agentvault rules export`
No flags.

## Roles (`roles` alias: `role`)

### `agentvault roles list`
Flags:
- `--json` (default: `false`)

### `agentvault roles show [name]`
No flags.

### `agentvault roles add [name]`
Flags:
- `--title <text>`
- `--description <text>`
- `--prompt <text>`
- `--rules <comma-separated-rule-names>`
- `--tags <comma-separated-tags>`

### `agentvault roles edit [name]`
Flags:
- `--title <text>`
- `--description <text>`
- `--prompt <text>`
- `--rules <comma-separated-rule-names>`
- `--tags <comma-separated-tags>`

### `agentvault roles remove [name]`
No flags.

### `agentvault roles init`
Flags:
- `--force` (default: `false`)

### `agentvault roles apply [role-name] [agent-name]`
No flags.

## Sessions (`session` aliases: `sess`, `workspace`)

### `agentvault session list`
Flags:
- `--json` (default: `false`)

### `agentvault session create [name]`
Flags:
- `--dir <path>`
- `--agents <comma-separated-agent-names>`
- `--role <role-name>`

### `agentvault session show [name]`
No flags.

### `agentvault session start [name]`
Flags:
- `--sequential` (default: `false`)
- `--agent <name>`
- `--dry-run` (default: `false`)

### `agentvault session stop [name]`
No flags.

### `agentvault session remove [name]`
No flags.

### `agentvault session activate [name]`
No flags.

### `agentvault session export [name] [file]`
No flags.

### `agentvault session import [file]`
No flags.

## Instructions (`instructions` alias: `inst`)

### `agentvault instructions list`
No flags.

### `agentvault instructions show [name]`
No flags.

### `agentvault instructions set [name]`
Flags:
- `--file <path>`
- `--content <text>`
- `--filename <target-file-name>`

### `agentvault instructions edit [name]`
No flags.

### `agentvault instructions remove [name]`
No flags.

### `agentvault instructions pull [directory]`
Flags:
- `--name <instruction-name>`
- `--file <filename>`

### `agentvault instructions push [directory]`
Flags:
- `--name <instruction-name>`

### `agentvault instructions diff [directory]`
No flags.

### `agentvault instructions scan [name]`
No flags.

## Shared config

### `agentvault config show`
No flags.

### `agentvault config set-prompt [prompt]`
No flags.

### `agentvault config add-mcp [name]`
Required:
- `--command <command>`

Optional:
- `--args <arg1,arg2,...>`

### `agentvault config remove-mcp [name]`
No flags.

## Sync

### `agentvault sync to [directory]`
Flags:
- `--agents-only` (default: `false`)
- `--provider <provider>`
- `--include-roles` (default: `true`)
- `--force` (default: `false`)

### `agentvault sync vault`
Flags:
- `--include-roles` (default: `true`)

### `agentvault sync preview`
Flags:
- `--provider <provider>`

## Generate

### `agentvault generate claude`
Flags:
- `--dry-run` (default: `false`)
- `--merge` (default: `true`)

### `agentvault generate codex`
Flags:
- `--dry-run` (default: `false`)
- `--merge` (default: `true`)
- `--project <path>`

### `agentvault generate env [output-file]`
Flags:
- `--format <dotenv|shell|json>` (default: `dotenv`)
- `--no-keys` (default: `false`)
- `--agent <name>`

### `agentvault generate mcp [output-file]`
Default output is Claude Desktop's config for the OS:
`~/Library/Application Support/Claude/claude_desktop_config.json` (macOS),
`%APPDATA%\Claude\claude_desktop_config.json` (Windows),
`~/.config/claude/claude_desktop_config.json` (Linux). `-` prints to stdout.
An existing file keeps every key except `mcpServers`, in order, its mode and (outside Windows) its owner; a file
that is not a JSON object is left alone and the command fails.
The file is replaced by a rename, so a hard link to it keeps the old content.

Flags:
- `--agent <name>`
- `--shared-only` (default: `false`)

### `agentvault generate all`
No flags.

## Setup (deprecated)

`setup export` and `setup import` are superseded by `agentvault export` and
`agentvault import`. They still work and still read and write the schema 1.x
bundle, but they only ever see the single config directory `--config` points at.

### `agentvault setup export [file]`
Flags:
- `--include-keys` (default: `false`): Include API keys; also resolves keys from env vars when vault key is empty.
- `--include-secrets` (default: `false`): Include sensitive asset files; requires `--confirm` or interactive consent when not encrypted.
- `--confirm` (default: `false`): Bypass interactive confirmation for sensitive export options (CI/scripted use).
- `--detect` (default: `false`)
- `--include-status` (default: `false`)
- `--encrypted` (default: `false`)
- `--plain` (default: `false`)

### `agentvault setup import [file]`
Flags:
- `--merge` (default: `false`): Maps onto `--strategy replace`.
- `--apply-provider-configs` (default: `false`)

Refuses a portable bundle holding more than one profile, since this command has no profile
selection; use `agentvault import <file> --profile NAME` instead.

### `agentvault setup show [file]`
No flags.

### `agentvault setup apply [directory]`
Flags:
- `--generate` (default: `false`)
- `--only <comma-separated-instruction-names>`

### `agentvault setup pull`
Flags:
- `--claude` (default: `false`)
- `--codex` (default: `false`)
- `--ollama` (default: `false`)

## Templates

Precedence, editing and custom templates: [templates.md](templates.md).

### `agentvault templates list`
Flags:
- `--repo <path>` (default: current directory)

### `agentvault templates show <name>`
Flags:
- `--repo <path>` (default: current directory)
- `--metadata` (default: `false`)

### `agentvault templates refresh`
Flags:
- `--force` (default: `false`)

## Portable export/import

### `agentvault export [file]`

Exports every agentvault setting to one portable bundle (schema 2.x). Run with no
arguments for an interactive wizard; every prompt defaults to "include it", so
pressing Enter through the wizard exports everything, encrypted, to
`<config dir>/exports/<host>-<timestamp>.avbundle`.

Every config directory on the machine that holds a `vault.enc` becomes a named
profile in the bundle. Discovery covers the active config dir, `~/.config/agentvault`,
`~/.agentvault`, sibling `agentvault*` directories (including hidden ones under `$HOME`), and every path in
`AGENTVAULT_CONFIG_DIRS`. Each profile's vault is unlocked separately, trying
`AGENTVAULT_PASSWORD` before prompting.

When stdin is not a terminal the wizard is skipped and the defaults apply.

Flags:
- `-y`, `--yes` (default: `false`): Accept all defaults, skip the wizard.
- `--profile <name>` (repeatable; default: every discovered profile)
- `--include-keys` (default: `true`): Include API keys, filling empty vault keys from the environment.
- `--include-secrets` (default: `true`): Include secret-bearing provider and asset file content.
- `--include-sessions` (default: `true`)
- `--include-templates` (default: `true`)
- `--include-provider-files` (default: `true`): each agent tool's user-level setup and the files it references. See "What a bundle carries" below.
- `--include-skills` (default: `true`)
- `--include-memory` (default: `false`): Claude project memory (`~/.claude/projects/*/memory`) and Codex memories (`~/.codex/memories`). The wizard asks every time; the answer defaults to no.
- `--include-status` (default: `false`): A quota reading is a snapshot, not a setting.
- `--detect` (default: `true`)
- `--project <dir>`: Also capture project-local instruction, workflow and skill assets.
- `--encrypt` (default: `true`)
- `--plain` (default: `false`): Plaintext JSON; turns encryption off.
- `--vault-only` (default: `false`): Write the legacy single-vault export instead.
- `--confirm` (default: `false`): Skip interactive confirmations.

Environment:
- `AGENTVAULT_PASSWORD`: Master password tried before prompting, per profile.
- `AGENTVAULT_EXPORT_PASSWORD`: Bundle password for non-interactive encrypted exports.
- `AGENTVAULT_CONFIG_DIRS`: Extra config directories to treat as profiles, separated by the OS path list separator (`:` on Unix, `;` on Windows).

#### What a bundle carries

| Tool | User level (`--include-provider-files`) |
|---|---|
| Claude Code | `~/.claude/settings.json`, `keybindings.json`, `CLAUDE.md`, `agents/`, `commands/`, `output-styles/`, `hooks/`, `skills/`; MCP servers from `~/.claude.json` (only the `mcpServers` key) |
| Codex | `~/.codex/config.toml`, `AGENTS.md`, `rules/`, `prompts/`, `skills/` |
| Gemini CLI | `~/.gemini/GEMINI.md`, `settings.json`, `commands/` |
| GitHub Copilot | `~/.copilot/config.json`, `mcp-config.json`, `agents/`; `~/.config/github-copilot/` |
| Cursor, Windsurf | `~/.cursor/mcp.json`, `~/.codeium/windsurf/mcp_config.json` |

Also carried:
- Every file a user-level instruction file names (backticked paths, markdown links, `@path` imports), followed through the files those name, up to 10 hops. `~/`, `$HOME/` and paths under the home directory are allowed. Anything outside the home directory is listed as declined.
- The scripts that `settings.json` runs from `hooks` and `statusLine`, when they are under the home directory.
- With `--project <dir>`: the instruction files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`, `CONVENTIONS.md`, ...), the rule directories (`.github/instructions/`, `.github/prompts/`, `.cursor/rules/`, `.windsurf/rules/`, `.gemini/commands/`, `.clinerules`), `.claude/agents/` and `.claude/commands/`, `skills/`, and every file or directory those name.
- A Claude Code plugin inventory. Import prints the `claude plugin marketplace add` and `claude plugin install` commands; it installs nothing itself.

Never carried:
- Login tokens: `~/.claude/.credentials.json`, `~/.codex/auth.json`, `~/.gemini/oauth_creds.json`, `~/.gemini/google_accounts.json`, `~/.config/github-copilot/hosts.json` and `apps.json`. The login keys of `~/.copilot/config.json` (`copilot_tokens`, `logged_in_users`, `last_logged_in_user`, any key named like a token, except counts and limits such as `max_tokens` and `tokenLimit`) are removed; the rest of that file is carried. They are not carried even with `--include-secrets`. Sign in on the other machine.
- A project's own settings (`.claude/settings.json`, `.mcp.json`, `.gemini/settings.json`, `.codex/config.toml`, `.cursor/mcp.json`, `.vscode/mcp.json`). They travel with the repository. The bundle lists them as `repository`.
- `.local.` files, unless named with `--include-local`.
- Anything else a reference names inside a tool's directory: only the setup in the table above, rule directories and prose files at the top of that directory are followed. Memory, session transcripts (`~/.claude/projects/`, `~/.codex/sessions/`, `~/.codex/archived_sessions/`), history, backups, caches and logs are declined, also when named through the target of a symlinked tool directory (`~/dotfiles/claude/todos/` with `~/.claude` linked there) or of a link at any depth inside it. Memory travels only with `--include-memory`.
- Credential files an instruction names, in any letter case and also through a symlink: `.env`, `.env.*` and `*.env` (templates such as `.env.example` are kept), keys and certificates (`.pem`, `.key`, `.p12`, ...), `.netrc`, `.npmrc`, `.git-credentials`, SSH keys, and anything under `.ssh`, `.gnupg`, `.aws`, `.kube`, `.docker` or `.azure`. They are listed as declined. So is a referenced file with a second hard link, also inside a named directory, and with `--include-secrets`: its other name cannot be checked.
- Stream suffixes (`.env::$DATA`). Export leaves them behind with a warning or a declined entry; import refuses them on every platform.

Windows names:
- Device names (`CON`, `aux.md`, `COM1.txt`) are carried, with an export warning that Windows cannot write them. Import on Windows refuses them; other platforms write them.
- `~N` names (`draft~2.md`) are ordinary names. On Windows, import checks the name of each file it opens and refuses a write whose name differs from the one asked for: an 8.3 short name such as `SETTIN~1.JSO` that opens an existing `settings.json`, or a trailing dot or space that Windows drops. A reference spelled as a short name is judged by its long name.
- Shell and login startup files (`.bashrc`, `.zshrc`, `.profile`, `.config/autostart`, `Library/LaunchAgents`, ...) and any other dotfile at the home root except `~/.claude.json`. Import refuses to write them from a bundle, too.

Settings files that can hold tokens are redacted without `--include-secrets`. So are `~/.claude/hooks/` scripts, files in project rule directories that are neither prose nor TOML, credential-named files in any carried directory, files with a second hard link inside a tool's directory, and referenced files that are not prose, such as scripts and config files; Markdown and text are carried as is.

Files referenced outside the tools' own directories are written back on import only when they are prose. They are written 0600 and never executable. `~/.claude.json` only gets its `mcpServers` key merged in. Referenced scripts and config files are staged under `<config dir>/imported-assets/provider/provider_home/` for review, and never written to the home directory: placed on `PATH` or named by a hook, a file from a bundle runs code.

Import prints plugin commands only for plugin IDs shaped like `name@marketplace` and sources shaped like `owner/repo` or an `https://` URL. Anything else is printed quoted, as a comment.

Stored prompt sessions are excluded from bundles: they are local run history holding
prompt and response text. Encryption follows the flags and the wizard answer, never the
output file's extension.

### `agentvault import [file]`

Imports a bundle. Portable bundles (schema 2.x), `setup export` bundles (schema 1.x)
and legacy vault exports are all accepted; the format is detected automatically.
With no file argument the newest export in `<config dir>/exports` is used.

Flags:
- `--strategy <merge|replace|mirror>` (default: `merge`)
  - `merge`: keep the existing vault value on every collision, add only what is missing.
  - `replace`: the bundle wins on collisions, local-only items are kept.
  - `mirror`: the bundle wins and local-only items are deleted, so the machine ends up
    matching the bundle exactly. Use this to converge several machines.
- `--profile <name|all>`: Which bundle profile to import. Required for a multi-profile
  bundle when stdin is not a terminal. `mirror` accepts a single profile only.
- `--list` (default: `false`): List the profiles in the bundle and exit.
- `--dry-run` (default: `false`): Report what would change without writing.
- `--apply-provider-configs` (default: `false`): Write provider configs and provider asset
  files back to the system after import.
- `--confirm` (default: `false`): Skip the confirmation prompt that guards `mirror`.

Environment:
- `AGENTVAULT_PASSWORD`: Master password tried before prompting.
- `AGENTVAULT_IMPORT_PASSWORD`: Bundle password for non-interactive imports.

Mirror deletes vault items that are not in the bundle. It asks for confirmation on a
terminal and refuses to run non-interactively without `--confirm`.

