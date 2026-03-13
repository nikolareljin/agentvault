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
  - Supported targets: `agents`, `instructions`, `rules`, `sessions`, `detected`, `commands`, `status`
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
- `status`
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

### `agentvault remove [name]`
Flags:
- `-f, --force` (default: `false`)

### `agentvault run [name]`
Flags:
- `--env` (default: `false`)

## Prompt and status

### `agentvault prompt [agent-name]`
Input:
- Without `--workflow` (default), one primary prompt source is required:
  - `--text <prompt>` or
  - `--file <path>` or
  - stdin
- With `--workflow`, the main task comes from workflow context and `--text`, `--file`, or stdin become optional operator notes.

Flags:
- `--text <prompt>`
- `--file <path>`
- `--workflow <name>`: `implement_issue|issue|implement_pr|pr|fix_pr`
- `--repo <path>`: repository path for workflow context (default: current directory)
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
- `--include-keys` (default: `false`)
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
