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

When the directory is a git checkout, the vault records it by remote (or root commits), so a later import or push can find the same repository at another path.

### `agentvault instructions push [directory]`
Flags:
- `--name <instruction-name>`

A directory-scoped instruction whose pattern names another known path of the same repository (its path on the machine a bundle came from, or an older checkout) applies to the directory too.

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
An existing file keeps every key, in order, its mode and (outside Windows) its owner. In `mcpServers`, a generated server replaces the one of the same name and a server only the file has stays; a file
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
- `--workdir <dir>` (repeatable): Record a folder of projects. Projects under it are recorded relative to it, so import can place them in the folder of the same name on the other machine.
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
- `~N` names (`draft~2.md`) are ordinary names. On Windows, import checks the name of each file it opens and refuses a write whose name differs from the one asked for: an 8.3 short name such as `SETTIN~1.JSO` that opens an existing `settings.json`. A reference spelled as a short name is judged by its long name.
- Shell and login startup files (`.bashrc`, `.zshrc`, `.profile`, `.config/autostart`, `Library/LaunchAgents`, ...) and any other dotfile at the home root except `~/.claude.json`. Import refuses to write them from a bundle, too.

Without `--include-secrets`, JSON settings files that can hold tokens (`~/.copilot/config.json` included) travel with their secret values blanked, as described below. Other settings files that can hold tokens are redacted whole. So are `~/.claude/hooks/` scripts, which are code and cannot be blanked field by field, files in project rule directories that are neither prose nor TOML, credential-named files in any carried directory, files with a second hard link inside a tool's directory, and referenced files that are not prose, such as scripts and config files. Import names each redacted file it could not write. Markdown and text are carried as is.

The vault's own copies are blanked the same way: the Claude settings `setup pull` stored (`env`, `apiKeyHelper`, MCP `headers` and the like; with a secret under a neutral key, such as `--token x` in a command, the settings are left out and import keeps this machine's), every `env` value and secret-shaped argument of shared and per-agent MCP servers, secret-shaped agent and Ollama base URLs, agent provider extras under a secret key or with a secret-shaped value, and secret keys of Codex and Ollama custom settings (these are dropped, not blanked). Import fills each blank from this machine's vault when the definition is unchanged, and otherwise leaves the value out and names it as `Left out`. An MCP server is unchanged when it keeps this machine's command, args and `env` names; Codex and Ollama custom settings when they are exactly what export makes of this machine's. For an agent this covers its API key too: the agent must keep this machine's provider, backend, base URL, provider settings and provider extras names, and a blank matches only a value export blanks, or the blank value is left out and named. The detected-agents summary drops `env`, `headers`, secret keys and secret-shaped values.

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
- `--dry-run` (default: `false`): Report what would change without writing. With `--apply-provider-configs` or `--apply-projects` it also lists each file it would write, back up and replace, keep, or leave out, and each command it would leave out. It writes no file, backup or directory, and asks nothing.
- `--apply-provider-configs` (default: `false`): Write provider configs and provider asset
  files back to the system after import.
- `--apply-projects` (default: `false`): Write the bundle's project files into `--project-root`, or, without it, into the confirmed match of the bundle's project (see below).
- `--project-root <dir>`: Where `--apply-projects` writes. The exporting machine's `--project` directory is rewritten to it in every file.
- `--map OLD=NEW` (repeatable): Rewrite another path prefix from the exporting machine. OLD and NEW must be absolute paths; NEW may start with `~/`. A `--map` always wins over a computed match.
- `--workdir-map OLD=NEW` (repeatable): Place a work dir from the bundle at NEW, an existing directory. Projects under it are matched by identity. The mapping is saved in the vault and reused by later imports (shown for confirmation). Naming a target inside a hidden folder confirms it, and the confirmation is saved: a later import reusing the same mapping does not ask again.
- `--accept-matches` (default: `false`): Apply computed project matches without a terminal. Ambiguous matches and targets inside hidden folders are still skipped.
- `--no-match` (default: `false`): Do not look for the bundle's projects; rewrite paths only by `--map`, `--project-root` and the home directory.
- `--keep-interpreter-allows` (default: `false`): Keep Claude permissions such as `Bash(bash:*)`, `Bash(python3:*)` or `Bash(*)`. By default they are left out on import and each one is reported. A Bash rule is kept only when it is a plain command line: literal words (letters, digits and `._/@%+,-`) separated by single spaces, an optional trailing `:*` or ` *`, and a command that is not an interpreter, wrapper or shell keyword. Any other Bash rule counts as a blanket grant: on the other machine such a rule lets any program run without asking, and nobody there granted it.
- `--keep-bundle-commands` (default: `false`): Keep the commands the bundle adds or changes. By default import keeps this machine's value, and names each one left out, for:
  - each `hooks` event, `statusLine`, `fileSuggestion`, credential helpers (`apiKeyHelper`, `awsAuthRefresh`, `awsCredentialExport`, `otelHeadersHelper`), `enabledPlugins` and `extraKnownMarketplaces`;
  - each MCP server (`mcpServers`, `servers`, Codex `mcp_servers`), of any type, that is new or differs in anything but its blanked secrets, unless it was picked (see `--mcp-servers`). With this flag every server is taken, apart from changed ones under `--on-conflict keep`;
  - code-loading variables in an `env` map (`NODE_OPTIONS`, `LD_PRELOAD`, `PYTHONSTARTUP`, `BASH_ENV`, `PATH` and the like), and variables that send requests elsewhere (any name containing `_URL`, `ENDPOINT` or `PROXY`, or ending in `_HOST`, such as `ANTHROPIC_BASE_URL` or `AWS_ENDPOINT_URL_BEDROCK_RUNTIME`; provider switches `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`, `ANTHROPIC_FOUNDRY_RESOURCE`; CA bundle settings such as `NODE_EXTRA_CA_CERTS`);
  - folder trust: Copilot `trusted_folders`, Gemini `security.folderTrust`, and Gemini `trustedFolders.json` (kept whole);
  - a JSON settings file that does not parse as a plain JSON object (comments, trailing data) and differs: it is not applied. When this machine's copy is the one that does not parse, it is kept as it is;
  - any other top-level key under which a `command`, `args`, `*Command` or `*Helper` field differs (Gemini `tools.discoveryCommand`, `tools.callCommand`);
  - scripts: a file under `hooks/`, or any file that is not prose or settings by extension (`.md`, `.txt`, `.rst`, `.json`, `.toml` and the like; extensionless files included), that is new or differs is not written;
  - trusted projects from the Codex provider config that are new or changed.

  A Codex `config.toml` is compared after parsing: if its `notify`, `model_providers`, `shell_environment_policy`, `projects`, `approval_policy`, `sandbox_mode` or `sandbox_workspace_write` (top level or under a profile), `hooks`, `otel`, any key ending in `url` or `endpoint` (`chatgpt_base_url`), or any command field, differ, only its MCP servers are applied (this machine's file is kept otherwise). If it does not parse, or its servers are not written as `[mcp_servers.<name>]` tables that can be edited one by one, it is not applied. The Codex provider config is applied as `config.toml` and `rules/<name>.md` files through the same checks; a rule name that is not a plain file name is left out, and local rules are no longer deleted.
- `--mcp-servers <a,b|all|none>`: Which MCP servers to take from the bundle, without asking. `all` and `none` stand alone; combined with names they are an error. A name no bundle server has is warned about. MCP servers in `mcpServers`, `servers`, `mcp.servers` (VS Code) and `context_servers` (Zed) of JSON settings (Claude `~/.claude.json` and `settings.json`, Gemini, Copilot, Cursor, Windsurf) and in Codex `config.toml` are merged one by one:
  - only on this machine: kept;
  - the same on both sides, apart from secrets the bundle blanked: this machine's definition stays;
  - changed: kept unless named here (or `all`). On a terminal import shows the masked difference and asks `[b]ackup + replace / [k]eep` (`ba`, `ka` for all remaining servers); the default and end of input keep this machine's server. A replaced server can get this machine's secret back after a yes;
  - only in the bundle: left out unless named here. On a terminal import lists them with type and command or URL; a row number selects or clears one, `a` selects all, an empty line accepts, `q` stops the import (files already written stay, with their backups). None is selected at first, and end of input picks none.

  A server from the bundle is offered only when every field is known (`type`, `command`, `args`, `url`, `httpUrl`, `env`, `headers`, `timeout`, `disabled`, `description`, and Codex `enabled`, `startup_timeout_sec`, `tool_timeout_sec`, `http_headers`, `bearer_token_env_var`, `env_http_headers`, `env_vars`; Zed `context_servers` also `source` and `settings`) and no secret-shaped value sits in `command`, `args`, `url`, `httpUrl`, the headers, a gated env variable or Zed `settings`. Any other server (`serverUrl`, `envFile`, `cwd`, `headersHelper`, ...) is not offered and is named; `--keep-bundle-commands` takes it. The pick list shows every field verbatim, nothing masked. The changed-server question shows the changed fields with secret values masked, then the bundle's whole definition verbatim. These checks fail closed, so some ordinary servers are also not offered or need the separate yes: any `%...%` pair counts as a variable reference (`%1%`, `%PROGRAM FILES%`) except the `%XX` escapes of a clean http(s) URL in the server's own `url` or `httpUrl` (`caf%C3%A9`; in `command`, `args`, env values, headers and `settings` every `%...%` pair counts, since a shell may expand `%CD%`; a `%25` or a bare `%` removes that exception, so any `%...%` pair in the URL counts), and a URL whose query uses `;` as a separator or holds a bad escape (`%zz`) is not offered. To take such a server, use `--keep-bundle-commands` or add it to this machine's config by hand. A picked or replaced server's command, args and URL are taken. Two things need a separate yes on a terminal and are refused without one (unless `--keep-bundle-commands`): a server that reads this machine's environment variables (`$VAR`, `${VAR}`, `${env:VAR}`, `$env:VAR`, `%VAR%` and any other `%...%` pair outside URL escapes, `bearer_token_env_var`, `env_http_headers`, `env_vars`; any other `$` or `%...%` counts too), asked with each variable named, even when this machine's server reads the same ones, and not taken on a no; and code-loading or request-routing env variables in `env`, Zed's `command.env` or any `env` object under Zed `settings` (environment under `settings` that cannot be gated by path, such as a non-object `env`, `environment` or `envVars`, an `env` under an array, a gated variable name anywhere else in `settings` (as a key: a known code-loading or routing name in any case, or another gated name spelled like an env name in upper case, ignoring surrounding spaces; lower-case keys such as `api_url` or `proxy` are ordinary settings; as `NAME=` in a string after any character that cannot be part of a name, in any case; or a known code-loading or routing name followed by `=` anywhere, even glued to a prefix such as `-eNODE_OPTIONS=`; only a shape-gated name such as `redirect_url=` inside the query of a plain http(s) URL, one with no whitespace or shell characters that prints back unchanged, is allowed), a settings key with non-ASCII letters, nested values inside an `env` object, or settings nested deeper than 64 levels, means the server is not offered and the report gives the reason; variable paths are printed with each part quoted; commands nested in `settings` are listed on their own lines) (the `env` rules below, such as `NODE_OPTIONS` or `*_URL`), left out and named on a no. Without the flag or a terminal nothing new is added and changed servers keep this machine's definition. A file whose only difference is the servers settled here is not asked about again; it is backed up and written. Import prints one `MCP servers:` line per file, naming once each server of the bundle that is new or changed: `added`, `replaced`, `kept local` (a changed server whose replacement was declined), `left out`, `left out variables` (each variable by path), `not offered` with the reason, and `not taken, reads this machine's variables`. A server only this machine has is kept and not listed; JSON files keep this machine's key order and server order, with new keys and servers after them in the bundle's order; `--dry-run` asks nothing and prints the plan (`would ask about` for questions it would ask on a terminal, including the separate yes for variables). Codex tables are edited in place: comments and other lines stay, a new server is appended, inserted tables use the file's line ending, and a server name with a quote, backslash, bracket or control character is refused. A layout that cannot be edited this way (dotted keys, inline tables) is named in the report and the whole-file rule decides.
- `--on-conflict <backup-replace|keep>`: What to do with a provider file, Claude project memory file or `--apply-projects` file that already exists and differs. Without it, import asks on a terminal and backs up and replaces otherwise; `--strategy merge` given explicitly means `keep`, `replace` and `mirror` mean `backup-replace`.
- `--confirm` (default: `false`): Skip the confirmation prompt that guards `mirror`.

Environment:
- `AGENTVAULT_PASSWORD`: Master password tried before prompting.
- `AGENTVAULT_IMPORT_PASSWORD`: Bundle password for non-interactive imports.

Mirror deletes vault items that are not in the bundle. It asks for confirmation on a
terminal and refuses to run non-interactively without `--confirm`.

#### Files that already exist

A provider file, Claude project memory file or `--apply-projects` file that exists and differs from the bundle's is a conflict. On a terminal import asks:

```
in /home/alex/.claude: local settings.json (changed 2026-10-18 09:12), bundle (changed 2026-10-19 14:30)
[b]ackup + replace / [k]eep existing (ba, ka: same for all remaining) [b]:
```

Without a terminal, or with `--on-conflict backup-replace`, the file is backed up and replaced. A backup is the file as it was before the import, at `<file>.bak-YYYYMMDD-HHMMSS` (with `-1`, `-2` added if that name exists; an existing file is never overwritten). Import lists every backup at the end. Export does not carry these backups.

A JSON provider file is merged by top-level key: the bundle's keys overwrite, keys only this machine has stay. A key only this machine has that holds a secret stays only when the bundle changes nothing else in the file, or on a yes to the prompt above.

Claude allow rules such as `Bash(find:*)`, `Bash(git:*)`, `Bash(make:*)`, `Bash(sed:*)`, `Bash(tar:*)`, `Bash(ssh:*)`, `Bash(docker:*)` and `Bash(kubectl:*)` are common and are kept, but import prints one WARNING block naming each with the reason: `find -exec`, `git -c core.pager=...`, `make` running any Makefile, `sed`'s `e` command and so on.

#### Moving to a machine with a different home directory

The bundle records the exporting machine's home directory and `--project` directory. On import, every text file is rewritten:
- each `--map` first;
- then the project directory to `--project-root`;
- then each confirmed project and work dir match (below);
- then the old home to this machine's home.

#### Finding projects by identity

A bundle records each project (the `--project` directory and the Codex trusted projects) with its work dir and git identity: the normalized remote (`github.com/owner/repo`; SSH, scp-like and HTTPS spellings of one remote are one identity, owner and repo compared without case on github.com and bitbucket.org only) and, except on Windows, the root commits. Import finds each work dir under this home by its path relative to home, then by name. A mapping saved by an earlier import is reused. A work dir it cannot find does not stop the import: its projects are left out and the rest is imported. On a terminal import then asks for each such work dir, offering the closest top-level home folders, and matches only that work dir's projects again; the answer is saved in the vault. Whatever is still missing is listed at the end under `Not imported`, with the `--workdir-map` line to add, and import exits non-zero. A target inside a hidden folder below home (`~/.work/x`) is asked about per entry (y, or another path) even with `--accept-matches`; without a terminal it counts as missing unless `--workdir-map` names it or a folder above it. Each question is asked once per import, whatever the number of profiles. A work dir confirmed in a hidden folder (y, a typed path or `--workdir-map`) is saved with its mapping and the identities of the checkouts then under it, read from this machine. It is not asked again while the mapping points at the same target and every bundle project under it is one of those checkouts, found in place (its own repository root, reached without a symlink below the target) with the same identity; then only those projects' paths are rewritten, not other paths under the work dir. Changing the target, a new or unidentified project, or a missing checkout asks again. A work dir row edited in the confirm list is saved as the new mapping and its projects are matched again there (a hidden target is confirmed first); skipping a row keeps what is saved. A bundle cannot carry such a confirmation. Import then scans the work dirs and home (three levels, no symlinks, no hidden folders, `node_modules` or `vendor`) for checkouts of the same repository.

Every computed match is shown and confirmed, even a unique one:

```
Projects in the bundle and where they go here:
  1. /home/alex/Projects -> /home/alex/src
       work dir
  2. /home/alex/Projects/app -> /home/alex/src/app
       score 130: same remote github.com/acme/app, same place in work dir, same name
Enter a row number to change its path, empty line to accept all, q to abort:
```

A row number edits that row's path (an existing absolute directory, `~/` allowed; a letter picks one of the candidates of an ambiguous row; `-` skips the row). Without a terminal the list is printed and nothing computed is used, and no project files are written, unless `--accept-matches` is given. Confirmed matches are recorded in the vault, so `instructions push` uses them too.

Rewrites match whole path segments, so `/home/alex` does not rewrite `/home/alexandra` or `/srv/home/alex`. Each spot is rewritten once, and a Windows path also matches its JSON spelling (`C:\\Users\\alex`), its forward-slash spelling (`C:/Users/alex`) and any letter case. A path moved between Windows and POSIX gets the new separators up to its end (`C:\Users\alex\bin\x` becomes `/home/alex/bin/x`). In a JSON file the path runs to the closing quote, spaces included, or to a space before a flag, a drive or another absolute path; in other text it ends at the first whitespace, so `My Docs\x` there keeps its backslash. Claude's per-project memory directories (`~/.claude/projects/-home-alex-...`) are renamed the same way, unless the new name is already taken by another directory in the bundle. Provider configs applied with `--apply-provider-configs` are rewritten too. A home path that no rewrite covers is reported with the `--map` to add. `--dry-run` prints every rewrite and changes nothing.

Secret values in JSON settings files are blanked on export without `--include-secrets`; the rest of the file travels. On import each blanked value is taken from the same file already on this machine. A value this machine does not have is left out and reported, never written as an empty string. A value is restored without asking only into the same definition. If the bundle changed the definition (an MCP server's URL or command, which also needs `--keep-bundle-commands`, the rest of `env` for a key in `env`, or any other field of the file for a top-level secret such as `apiKeyHelper`), import shows the old and new fields, with secrets masked, and asks once per definition: `restore local secret into changed server? [y/N]`. Without a terminal the answer is no, and the warning names the backup that holds the old value. Only listed fields under a secret key, `env` or `headers` count as secrets; any other field the bundle lists must still match. A file whose secrets sit under keys that do not say so is redacted whole, as before. That covers `args` such as `--token x` or `-H "X-API-Key: x"`, a command string with `--token x` in it, credentials in a URL, and known token prefixes. A JSON file that is a credential by name or location, such as `.docker/config.json` or anything under `.aws`, is redacted whole too.

`--apply-projects` writes prose and the files agent tools read as instructions, the project's own `AGENTS.md` or `CLAUDE.md` included:
- Never written: `.git/`, `.githooks/`, `.husky/`, `.github/workflows/`, repository settings and credential files.
- Other non-prose files, such as scripts, stay staged in `<config dir>/imported-assets/project/`.
- An existing file that differs is settled by `--on-conflict` (see below).
- Nothing is written executable. The execute bit is kept only for a tool's own `hooks/` scripts.

