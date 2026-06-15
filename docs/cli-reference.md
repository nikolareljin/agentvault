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
then falls back to `/health` (llm-gateway-helpers shape). Infers capability tags from model names.
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
Flags:
- `--agent <name>`
- `--shared-only` (default: `false`)

### `agentvault generate all`
No flags.

## Setup

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
- `--merge` (default: `false`)
- `--apply-provider-configs` (default: `false`)

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

## Legacy export/import

### `agentvault export [file]`
Flags:
- `--plain` (default: `false`)

### `agentvault import [file]`
Flags:
- `--plain` (default: `false`)
