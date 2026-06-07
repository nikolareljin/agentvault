# AgentVault Detailed Usage Guide

This document is a full CLI + TUI reference for AgentVault, including command parameters, flags, and tab actions.

Related docs: [Docs Index](./README.md) | [CLI Reference](./cli-reference.md) | [TUI Reference](./tui-reference.md) | [Workflows](./workflows.md)

## 1. Global CLI Usage

```bash
agentvault [global flags] [command] [subcommand] [args] [flags]
```

### Global flags

- `--config <dir>`: Use custom config directory instead of default `~/.config/agentvault`.
- `-t, --tui [target]`: Launch interactive TUI (also the default when no command is provided).
  - Supported targets: `agents`, `instructions`, `rules`, `sessions`, `detected`, `commands`, `status`, `about`.
  - With command routing, `agentvault <command> -t` opens TUI on the command's matching tab and skips direct command execution.
- `-p, --prompt-mode[=true|false]`: Enter interactive prompt mode directly (submit/cancel/exit loop).

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
- `[agent-name]` is required as the first positional argument, **unless** `--auto` is used.
- Use one of:
  - `--text <prompt>`
  - `--file <path>`
  - stdin pipe
- `--text` and `--file` are mutually exclusive.

Common error:
- `agentvault prompt --text "create a demo app in Scala that says 'Hello World'"`
- This fails with `accepts 1 arg(s), received 0` because `[agent-name]` is missing.
- Run `agentvault list` to find available agent names, or use `--auto`.

Flags:
- `--text <prompt>`
- `--file <path>`
- `--auto` (default: `false`): Let AgentVault select the best agent via routing — no positional agent argument needed.
- `--json` (default: `false`): Machine-readable response + record.
- `--optimize` (default: `true`): Enable prompt rewrite/structuring.
- `--optimize-profile <profile>` (default: `auto`): `auto|generic|ollama|codex|copilot|claude`.
- `--optimize-ollama` (default: `true`): Deprecated compatibility switch.
- `--dry-run` (default: `false`): Show effective prompt, do not execute.
- `--validate-only` (default: `false`): Validate provider/backend connectivity without sending a prompt.
- `--no-log` (default: `false`): Disable run history write.
- `--history-file <path>`: Override default `~/.config/agentvault/prompt-history.jsonl`.
- `--timeout <duration>` (default: `5m`): Provider call timeout.

Routing flags (used when `--auto` is set or router mode overrides are desired):
- `--router <mode>`: `heuristic|langgraph|local-ai|llm-router`
- `--importance <level>`: `low|medium|high|critical`
- `--deadline <level>`: `immediate|normal|background`
- `--prefer-local`, `--prefer-fast`, `--prefer-low-cost`, `--local-only`
- `--local-ai-model`, `--local-ai-url`
- `--llm-router-url`, `--llm-router-model`, `--llm-router-timeout`
- `--llm-router-model-path`, `--llm-router-context-size`, `--llm-router-threads`, `--llm-router-gpu-layers`

Runtime value precedence:
- local agent value in vault
- environment fallback (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `OLLAMA_HOST`)
- default fallback (`http://localhost:11434` for Ollama base URL)

Execution behavior:
- Codex runs in agentic workspace-write mode.
- Claude runs with `--permission-mode auto`.
- Gemini runs with `--approval-mode auto_edit`.
- Gemini receives both `GEMINI_API_KEY` and `GOOGLE_API_KEY` from AgentVault runtime config.
- Ollama stays on the HTTP prompt path.
- Prompt text alone does not change repository context.
- Use `--workflow ... --repo ...` for repo-aware context generation.
- Use `--workspace ...` when you need agentic edits to target specific directory explicitly.

TUI agent detail renders effective model/API key/base URL with source tags (`local`, `env`, `default`).

Examples:
```bash
# list configured agents and pick one name
agentvault list

# codex example
agentvault prompt my-codex --text "create a demo app in Scala that says 'Hello World'"

# claude example
agentvault prompt my-claude --text "refactor this service safely"

# gemini example
agentvault prompt my-gemini --text "implement this feature and add tests"

# ollama example
agentvault prompt my-ollama --text "create a demo app in Scala that says 'Hello World'" --optimize-profile ollama
```

### `agentvault -p`
Enter interactive prompt mode immediately.

Behavior:
- Prompts for agent selection (unless only one agent exists).
- Supports submit (`Enter`), cancel (`/cancel`), and exit (`/exit`, `quit`, `:q`).
- Per-message token usage is printed to stderr after each response when available, typically for providers/versions that return usage metadata.
- On exit, a **session summary** is printed showing cumulative input, cached-input, output, reasoning, and total tokens when usage metadata is available.
- Can optionally log each execution to `~/.config/agentvault/prompt-history.jsonl` when history logging is enabled.
- Can optionally persist transcript/session metadata (including aggregate token totals) in encrypted vault state on exit.

### `agentvault status`
Show provider usage/quota status report.

Flags:
- `--json` (default: `true`)
- `--no-vault` (default: `false`): Skip vault unlock and only report provider status.
- `--vault-password-env <ENV_VAR>` (default: `AGENTVAULT_PASSWORD`): Non-interactive unlock env var.
- `--cost-report` (default: `false`): Include estimated cost breakdown aggregated from `prompt-history.jsonl`. Shows per-provider spend and fires budget alerts when spend exceeds 80% of `MonthlyBudgetUSD`.

Examples:
```bash
# Basic status
agentvault status --json

# Non-interactive with cost breakdown
AGENTVAULT_PASSWORD=... agentvault status --cost-report --json

# Human-readable cost report
agentvault status --cost-report
```

## 3.3b Intelligent Routing

### Routing modes

| Mode | Description | External requirement |
|------|-------------|---------------------|
| `heuristic` | Scores agents by capabilities, tiers, priority | None |
| `langgraph` | Python sidecar for graph-based routing | `python/langgraph_router.py` |
| `local-ai` | Sends prompt to local Ollama for classification, then scores | Ollama running |
| `llm-router` | Calls OpenAI-compat HTTP server OR embedded GGUF engine | Server or `make build-bitnet` + GGUF file |

### `agentvault route`
Inspect routing decision without executing the prompt.

```bash
# Basic inspection
agentvault route --text "write a sorting algorithm"

# With importance and deadline signals
agentvault route --text "critical production fix" --importance critical --deadline immediate
agentvault route --text "background analysis job" --importance low --deadline background

# Local-AI classification (requires Ollama)
agentvault route --router local-ai --text "implement JWT auth"

# LLM-router via HTTP server
agentvault route --router llm-router \
  --llm-router-url http://localhost:8080 \
  --text "refactor this service"

# LLM-router via embedded engine (agentvault-bitnet binary)
./agentvault-bitnet route \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "write a parser in Go"
```

### Embedded BitNet engine

The `llm-router` mode supports in-process GGUF inference — no external server needed.
Requires a one-time build step:

```bash
# 1. Build llama.cpp static library (~5 min)
make build-llama

# 2. Build the bitnet-enabled binary
make build-bitnet          # produces ./agentvault-bitnet

# 3. Download model (~400 MB)
./agentvault-bitnet routing-model download

# 4. Route via embedded engine
./agentvault-bitnet route \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "implement binary search"
```

The default `make build` binary uses a pure-Go stub: `--llm-router-model-path` is accepted
but the engine falls back to heuristic routing with an explanatory reason string.

## 3.3c Capability Registry

### `agentvault capability`
Manage the model capability registry used by all routing modes to augment agent scoring.

```bash
# List all entries
agentvault capability list
agentvault capability list --json

# Add manually
agentvault capability add \
  --endpoint http://localhost:11434 \
  --model llama3.1:8b \
  --context 8192 \
  --caps code,general

# Auto-discover from endpoint (/v1/models or /health)
agentvault capability discover --endpoint http://localhost:11434
agentvault capability discover --endpoint http://localhost:8080 --timeout 30s

# Remove one entry
agentvault capability remove \
  --endpoint http://localhost:11434 \
  --model llama3.1:8b
```

Capability tags inferred from model names during discovery:
- `code` — model name contains `code`, `codex`, `coder`, `starcoder`, `deepseek-coder`
- `vision` — contains `vision`, `vl`, `llava`, `visual`
- `embedding` — contains `embed`
- `reasoning` — contains `reasoning`, `think`, `r1`
- `general` — always added

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
- `--include-keys` (default: `false`): Include API keys. Also captures keys sourced from environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_API_KEY`) when the vault-stored key is empty.
- `--include-secrets` (default: `false`): Include sensitive provider asset file content. **Requires interactive confirmation or `--confirm` when `--encrypted` is not set.**
- `--confirm` (default: `false`): Skip the interactive confirmation prompt for sensitive export options. Use in CI/scripted contexts.
- `--detect` (default: `false`)
- `--include-status` (default: `false`): Include provider usage/quota snapshot.
- `--encrypted` (default: `false`)
- `--plain` (default: `false`)

The export summary includes a per-agent key status table showing whether each agent's key was included from the vault, resolved from an environment variable, redacted, or shown as `[no key found]` when `--include-keys` is set and neither the vault nor environment provides a key.

`setup export` also includes workflow templates from AgentVault config storage (default: `~/.config/agentvault/templates/`; honors `XDG_CONFIG_HOME` and `--config`) with template version metadata.

### `agentvault setup import [file]`
Flags:
- `--merge` (default: `false`)
- `--apply-provider-configs` (default: `false`)

`setup import` restores shared router settings from exported bundles. Without `--merge`, an existing router config is preserved; with `--merge`, the imported router config replaces it.

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

## 3.12 Workflow templates

Workflow template precedence:
1. repository-local override (`./implement_issue.txt`, `./implement_pr.txt`, `./add_issue.txt`)
2. AgentVault config storage (default: `~/.config/agentvault/templates/`; honors `XDG_CONFIG_HOME` and `--config`)
3. built-in defaults (safe fallback with warning)

### `agentvault templates list`
List effective templates and their source.

### `agentvault templates show <name>`
Show effective template content by key or filename.
Use `agentvault templates show add_issue` to inspect the git-lantern-compatible TODO authoring template, including the embedded `implement_issue` and `implement_pr` reusable checklist modules.

### `agentvault templates refresh`
Initialize or refresh config-stored templates from built-in defaults.

## 3.13 Legacy vault export/import commands

### `agentvault export [file]`
Flags:
- `--plain` (default: `false`): Export plaintext JSON.

### `agentvault import [file]`
Flags:
- `--plain` (default: `false`): Import plaintext JSON.

## 4. TUI Detailed Reference

Launch:

```bash
agentvault
# optional explicit flag:
agentvault --tui
# or short:
agentvault -t
# direct target:
agentvault -t detected
# about tab:
agentvault -t about
# infer target from command:
agentvault detect add -t
```

Tabs:
1. Agents
2. Instructions
3. Rules
4. Sessions
5. Detected
6. Commands
7. Status
8. About

## 4.1 Global TUI controls

- `Tab`: next tab
- `Shift+Tab`: previous tab
- `1-8`: jump to tab
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
agentvault
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

Provider note:
- The optimization profile influences prompt shape.
- Execution mode is provider-specific: Codex uses workspace-write auto mode, Claude uses `permission-mode auto`, and Gemini uses `approval-mode auto_edit`.

## 5.4 Machine-readable orchestration checks

```bash
agentvault status --json
agentvault status --no-vault --json
AGENTVAULT_PASSWORD='***' agentvault status --json

# With cost breakdown
AGENTVAULT_PASSWORD='***' agentvault status --cost-report --json
```

## 5.5 Cross-machine sync

```bash
# source machine
agentvault setup export team.bundle --encrypted --include-status

# target machine
agentvault init
agentvault setup import team.bundle --merge --apply-provider-configs
```

## 5.6 Automatic routing with intelligent selection

```bash
# Heuristic routing (default)
agentvault prompt --auto --text "implement and test this Go refactor"

# LangGraph sidecar
export AGENTVAULT_LANGGRAPH_ROUTER_CMD="./python/langgraph_router.py"
agentvault prompt --auto --router langgraph --text "choose target for this task"

# Local Ollama classification
agentvault prompt --auto --router local-ai --text "implement JWT authentication"

# Embedded engine (bitnet binary)
./agentvault-bitnet prompt --auto \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "refactor this service to use interfaces"

# Routing with importance/deadline
agentvault prompt --auto --importance critical --deadline immediate \
  --text "fix production outage in auth service"
agentvault prompt --auto --importance low --deadline background \
  --text "add docstrings to utility module"
```

## 5.7 Cost tracking and capability registry setup

```bash
# First, populate capability registry from running endpoints
agentvault capability discover --endpoint http://localhost:11434   # Ollama
agentvault capability discover --endpoint http://localhost:8080    # llama-server

# Manually add an entry with context window override
agentvault capability add \
  --endpoint http://localhost:11434 \
  --model llama3.1:70b \
  --context 32768 \
  --caps code,general,reasoning

# Run some prompts — cost is tracked automatically per execution
agentvault prompt my-claude --text "review this PR"
agentvault prompt local-ollama --text "generate unit tests"

# View cost breakdown
agentvault status --cost-report
# Shows: total estimated cost, per-provider breakdown, budget alerts (>80% of monthly budget)
```

## 5.8 Embedded inference engine setup (one-time)

```bash
# Build llama.cpp static library (requires cmake, gcc, ~2 GB temp disk)
make build-llama

# Build bitnet-enabled agentvault binary
make build-bitnet
# Produces: ./agentvault-bitnet

# Download the BitNet routing model (~400 MB)
./agentvault-bitnet routing-model download
# Saved to: ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf

# Verify everything is ready
./agentvault-bitnet routing-model status
# Expected:
#   embedded inference: enabled
#   model file:         ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf
#   model size:         ~400 MB

# Use embedded engine for routing (no server needed)
./agentvault-bitnet route \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "write a binary search implementation"
```
