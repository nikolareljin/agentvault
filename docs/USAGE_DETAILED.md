# AgentVault Detailed Usage Guide

This document is a full CLI + TUI reference for AgentVault, including command parameters, flags, and tab actions.

## 1. Global CLI Usage

```bash
agentvault [global flags] <command> [subcommand] [args] [flags]
```

### Global flags

- `--config <dir>`: Use custom config directory instead of default `~/.config/agentvault`.
- `-t, --tui`: Launch interactive TUI.

## 2. Top-Level Commands

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

## 3. Command Reference (All Parameters)

## 3.1 Vault lifecycle

### `agentvault init`
Initialize encrypted vault. No flags.

### `agentvault unlock`
Unlock vault in current terminal session flow. No flags.

### `agentvault version`
Show version/build metadata. No flags.

## 3.2 Agent discovery and CRUD

### `agentvault detect`
Detect installed local agent CLIs.

Flags:
- `--json` (default: `false`): JSON output.
- `--verbose` (default: `false`): Include detailed detected settings/config files.

### `agentvault detect add`
Auto-add detected agents to vault.

Flags:
- `--force` (default: `false`): Overwrite/update matching existing vault agents.

### `agentvault add [name]`
Add new agent record.

Required flags:
- `-p, --provider <provider>` (required)

Optional flags:
- `-m, --model <model>`
- `-k, --api-key <key>`
- `--base-url <url>`
- `--system-prompt <text>`
- `--task-desc <text>`
- `--tags <comma-separated>`

### `agentvault list`
List agents in vault.

Flags:
- `--json` (default: `false`)
- `--show-keys` (default: `false`): Include API keys in output.

### `agentvault edit [name]`
Update agent fields.

Flags (all optional, only changed flags are applied):
- `-p, --provider <provider>`
- `-m, --model <model>`
- `-k, --api-key <key>`
- `--base-url <url>`
- `--system-prompt <text>`
- `--task-desc <text>`
- `--tags <comma-separated>`
- `--role <role-name>`
- `--disable-rules <comma-separated-rule-names>`

### `agentvault remove [name]`
Delete agent from vault.

Flags:
- `-f, --force` (default: `false`): Skip confirmation.

### `agentvault run [name]`
Show effective resolved configuration for one agent.

Flags:
- `--env` (default: `false`): Print shell exports instead of JSON.

## 3.3 Prompt Gateway and status

### `agentvault prompt [agent-name]`
Route prompt through AgentVault (gateway) to provider, with optimization + logging.

Input rules:
- Use one of:
  - `--text <prompt>`
  - `--file <path>`
  - stdin pipe
- `--text` and `--file` are mutually exclusive.

Flags:
- `--text <prompt>`
- `--file <path>`
- `--json` (default: `false`): Machine-readable response + record.
- `--optimize` (default: `true`): Enable prompt rewrite/structuring.
- `--optimize-profile <profile>` (default: `auto`): `auto|generic|ollama|codex|copilot|claude`.
- `--optimize-ollama` (default: `true`): Deprecated compatibility switch.
- `--dry-run` (default: `false`): Show effective prompt, do not execute.
- `--no-log` (default: `false`): Disable run history write.
- `--history-file <path>`: Override default `~/.config/agentvault/prompt-history.jsonl`.
- `--timeout <duration>` (default: `5m`): Provider call timeout.

### `agentvault status`
Show provider usage/quota status report.

Flags:
- `--json` (default: `true`)
- `--no-vault` (default: `false`): Skip vault unlock and only report provider status.
- `--vault-password-env <ENV_VAR>` (default: `AGENTVAULT_PASSWORD`): Non-interactive unlock env var.

## 3.4 Rules

Aliases:
- `rules` also available as `rule`.

### `agentvault rules list`
Flags:
- `--all` (default: `false`): Include disabled rules.
- `--json` (default: `false`)

### `agentvault rules show [name]`
No flags.

### `agentvault rules add [name]`
Flags:
- `--description <text>`
- `--content <text>`
- `--category <name>` (default: `general`)
- `--priority <int>` (default: `50`)

Note: `--content` is required logically by behavior (validated in command logic).

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
- `--force` (default: `false`): Add defaults even when rules already exist.

### `agentvault rules export`
No flags.

## 3.5 Roles

Aliases:
- `roles` also available as `role`.

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

Note: `--prompt` is required logically by behavior (validated in command logic).

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

## 3.6 Sessions

Aliases:
- `session` also available as `sess`, `workspace`.

### `agentvault session list`
Flags:
- `--json` (default: `false`)

### `agentvault session create [name]`
Flags:
- `--dir <path>` (default: current dir)
- `--agents <comma-separated-agent-names>` (default: all configured agents)
- `--role <role-name>`

### `agentvault session show [name]`
No flags.

### `agentvault session start [name]`
Flags:
- `--sequential` (default: `false`): Start one-by-one instead of parallel.
- `--agent <name>`: Start one specific session agent.
- `--dry-run` (default: `false`): Preview only.

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

## 3.7 Instruction management

Aliases:
- `instructions` also available as `inst`.

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
- `--name <instruction-name>`: Pull a specific logical instruction key.
- `--file <filename>`: With `--name`, specify source file name.

### `agentvault instructions push [directory]`
Flags:
- `--name <instruction-name>`: Push only one instruction.

### `agentvault instructions diff [directory]`
No flags.

### `agentvault instructions scan [name]`
No flags.

## 3.8 Shared config

### `agentvault config show`
No flags.

### `agentvault config set-prompt [prompt]`
No flags.

### `agentvault config add-mcp [name]`
Required flags:
- `--command <command>` (required)

Optional flags:
- `--args <arg1,arg2,...>` (string-slice)

### `agentvault config remove-mcp [name]`
No flags.

## 3.9 Sync

### `agentvault sync to [directory]`
Flags:
- `--agents-only` (default: `false`): Only `AGENTS.md`.
- `--provider <provider>`: Generate only provider-specific files.
- `--include-roles` (default: `true`)
- `--force` (default: `false`): Overwrite existing files.

### `agentvault sync vault`
Flags:
- `--include-roles` (default: `true`)

### `agentvault sync preview`
Flags:
- `--provider <provider>`

## 3.10 Generate

### `agentvault generate claude`
Flags:
- `--dry-run` (default: `false`)
- `--merge` (default: `true`)

### `agentvault generate codex`
Flags:
- `--dry-run` (default: `false`)
- `--merge` (default: `true`)
- `--project <path>`: Add trusted project.

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

## 3.11 Setup (cross-machine full bundle)

### `agentvault setup export [file]`
Flags:
- `--include-keys` (default: `false`)
- `--detect` (default: `false`)
- `--include-status` (default: `false`): Include provider usage/quota snapshot.
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
- `--generate` (default: `false`): Also generate `.env.agents` / provider configs.
- `--only <comma-separated-instruction-names>`

### `agentvault setup pull`
Flags:
- `--claude` (default: `false`)
- `--codex` (default: `false`)
- `--ollama` (default: `false`)

## 3.12 Legacy vault export/import commands

### `agentvault export [file]`
Flags:
- `--plain` (default: `false`): Export plaintext JSON.

### `agentvault import [file]`
Flags:
- `--plain` (default: `false`): Import plaintext JSON.

## 4. TUI Detailed Reference

Launch:

```bash
agentvault --tui
# or
agentvault -t
```

Tabs:
1. Agents
2. Instructions
3. Rules
4. Sessions
5. Detected
6. Commands
7. Status

## 4.1 Global TUI controls

- `Tab`: next tab
- `Shift+Tab`: previous tab
- `1-7`: jump to tab
- `j/k` or `Down/Up`: move selection
- `Enter`: open detail
- `Esc`: back from detail/modal
- `q` / `Ctrl+C`: quit
- `?`: help
- `r`: refresh vault/detected/provider data

## 4.2 Tab-by-tab actions

### Tab 1: Agents

Views:
- Agent list
- Agent detail

Actions:
- `/`: start search filter mode
- `Enter`: open selected agent detail
- `d`: delete selected agent (with confirmation)

### Tab 2: Instructions

Views:
- Instruction list
- Instruction detail

Actions:
- `Enter`: open selected instruction detail
- `e`: edit selected instruction in external editor (`$EDITOR`, then `nano/vi/vim` fallback)
- `d`: delete selected instruction (with confirmation)

### Tab 3: Rules

Views:
- Rule list
- Rule detail

Actions:
- `Enter`: open selected rule detail
- `d`: delete selected rule (with confirmation)

### Tab 4: Sessions

Views:
- Session list
- Session detail

Actions:
- `Enter`: open selected session detail

### Tab 5: Detected

View:
- Installed detected agents and whether they are in vault

Actions:
- `a`: add selected detected agent to vault

### Tab 6: Commands

Two modes exist here:

1. CLI command bridge:
- `:` (or `;`): type command and run `agentvault <...>` from TUI
- `Enter`: execute
- `Esc`: cancel command mode

2. Prompt Gateway flow:
- `g`: start gateway wizard

Prompt Gateway steps:
1. Select agent (`j/k`, `Enter`)
2. Input prompt (`type`, `Backspace`, `Enter`)
3. Preview rewritten prompt (`y`/`Enter` confirm, `n` edit, `Esc` back)
4. Run and wait for result
5. View result and usage; post-actions:
   - `s`: switch agent and start next interaction
   - `e`: edit/retry prompt
   - `Esc`: exit gateway

### Tab 7: Status

Read-only operational overview:
- vault path and object counts
- provider config status
- detected agent summary

No tab-specific action keys.

## 4.3 Confirmation and modal behavior

Delete confirmation modal:
- `y`: confirm delete
- `n` or `Esc`: cancel

Search mode (Agents tab):
- type to filter
- `Enter`: apply filter and exit search mode
- `Esc`: cancel search mode

## 5. Practical command bundles

## 5.1 Daily startup

```bash
agentvault -t
```

Use Commands tab + `g` for prompt gateway workflow.

## 5.2 Add and use an Ollama agent with prompt optimization

```bash
agentvault add local-ollama --provider ollama --model llama3.1 --base-url http://localhost:11434
agentvault prompt local-ollama --text "Implement JWT middleware" --optimize --optimize-profile ollama
```

## 5.3 Use Codex/Copilot-style optimization profile

```bash
agentvault prompt my-codex --text "Refactor this service safely" --optimize-profile codex
agentvault prompt my-copilot --text "Add tests for these edge cases" --optimize-profile copilot
```

## 5.4 Machine-readable orchestration checks

```bash
agentvault status --json
agentvault status --no-vault --json
AGENTVAULT_PASSWORD='***' agentvault status --json
```

## 5.5 Cross-machine sync

```bash
# source machine
agentvault setup export team.bundle --encrypted --include-status

# target machine
agentvault init
agentvault setup import team.bundle --merge --apply-provider-configs
```
