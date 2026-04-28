# Changelog

## [Unreleased]

## [0.10.0] - 2026-04-28

### Added
- **Session token summary in prompt mode**: `agentvault -p` now prints a cumulative token summary on exit showing total input, cached-input, output, reasoning, and total tokens across all messages in the session. Per-message token counts were already shown; this adds the session-level aggregate. The summary is also persisted to vault state via a new `total_token_usage` field on `PromptSession`. Closes #6.
- **Env-var API key export**: `agentvault setup export --include-keys` now also captures API keys that were set via environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, and Gemini fallback `GOOGLE_API_KEY`) when the vault-stored key for an agent is empty. This preserves credentials that were configured through the shell environment rather than stored directly in the vault. Closes #8.
- **Confirmation gate for plaintext sensitive export**: `agentvault setup export --include-secrets` without `--encrypted` now requires explicit confirmation — either an interactive `y/N` prompt or the new `--confirm` flag for scripted/CI use. Previously only a warning was printed; the export proceeded unconditionally. Closes #8.
- **Per-agent key status in export summary**: `agentvault setup export` output now includes a per-agent key status table showing `[vault key included]`, `[env key included]`, `[no key needed]`, `[no key found]`, or `[redacted]` for each exported agent, with `[no key found]` possible when `--include-keys` is requested but no key is available.

### Changed
- `internal/agent.PromptSession` gained a `TotalTokenUsage *PromptTokenUsage` field (JSON: `total_token_usage`) for aggregate session token data. The field is omitempty and fully backwards-compatible.
- `agentvault setup export --include-keys` flag description updated to mention env-var key resolution.

## [0.9.0] - 2026-04-22

### Added
- **Live streaming output**: `agentvault prompt` now streams CLI agent subprocess output (Claude, Codex, Gemini) live to the terminal via `io.MultiWriter`. Claude and Gemini omit `--output-format json` in stream mode so tool calls, file edits, and in-progress actions are visible as they happen. Streaming is automatically disabled in `--json` mode or when stdout is not a terminal; use `--no-stream` to force buffered mode.
- **Importance and deadline routing flags**: New `--importance low|medium|high|critical` and `--deadline immediate|normal|background` flags on both `agentvault prompt` and `agentvault route`. These feed directly into routing scoring — `critical` importance penalizes local targets and strongly prefers cloud runners; `immediate` deadline boosts low-latency agents; `background` deadline favors low-cost targets.
- **Local-AI router mode**: New `--router local-ai` mode sends the prompt to a local Ollama instance for structured classification (complexity 1–10, task type, urgency, privacy sensitivity, tool need) before routing. The result enriches intent detection and scoring. Gracefully falls back to heuristic with mode `local-ai-fallback` if Ollama is unreachable. Configurable via `--local-ai-model` and `--local-ai-url`.
- **Routing transparency**: Routing decisions now print the selected agent, runner, mode, task class, and top reasons before execution. `agentvault route` output now includes Mode, Importance, and Deadline fields.

### Changed
- `RouterConfig.WithDefaults` no longer forces `prefer-local` when `importance` or `deadline` is explicitly set, since those signals already express routing intent.

## [0.8.1] - 2026-04-17

### Fixed
- Codex prompt execution now automatically adds `--skip-git-repo-check` when AgentVault is launched from a directory outside any Git worktree, which fixes TUI and `prompt` failures from trusted non-repository paths such as `/home/nikos/Projects`.
- Codex Prompt Gateway and `agentvault prompt` now launch Codex in agentic workspace-write mode instead of a bare one-shot exec, avoiding read-only chat-style behavior when edits are expected.
- Claude and Gemini prompt execution now use provider-specific agentic approval modes in both Prompt Gateway and `agentvault prompt`, instead of plain read/respond execution flags.
- Prompt Gateway now shows a live running indicator with elapsed time and agent context while waiting for provider completion, instead of a static "Running prompt..." screen.

## [0.8.0] - 2026-03-24

### Added
- `agentvault setup export` now supports `--agent`, `--project`, and `--include-secrets` for portable single-agent bundles with project-local instructions, workflow templates, and skill assets.
- Setup bundles now carry explicit `provider_files`, `project_files`, `instruction_overrides`, and `skill_assets` sections with relocatable path metadata and redaction markers for sensitive content.
- Added an intelligent prompt router with new `agentvault route` inspection command and `agentvault prompt --auto` execution path.
- Added per-agent routing metadata for capabilities, latency/cost/privacy tiers, and routing priority.
- Added optional Python LangGraph sidecar support via `python/langgraph_router.py` and `AGENTVAULT_LANGGRAPH_ROUTER_CMD`.

### Changed
- `agentvault setup show` now summarizes portable asset sections and reports sensitive or redacted bundle content.
- Prompt execution now resolves provider-agnostic runner targets before execution, including OpenAI HTTP support alongside existing Ollama, Codex, and Claude paths.

## [0.6.0] - 2026-03-14

### Added
- Expanded built-in workflow templates to use the current `2.0` issue/PR implementation bodies.
- Upgraded the built-in `add_issue` template to generate git-lantern-compatible TODO entries with explicit input/output contracts, deterministic ID allocation, and embedded reusable `implement_issue` / `implement_pr` checklists.

## [0.5.2] - 2026-03-11

### Added
- TUI About tab with direct profile links for GitHub and LinkedIn.
- `--tui about` target support and updated tab navigation/help text for the new eighth tab.
- `templates` command group with `list`, `show`, and `refresh` for workflow templates (`implement_issue.txt`, `implement_pr.txt`, `add_issue.txt`).
- New workflow template storage under AgentVault config (`~/.config/agentvault/templates/`) with metadata (`metadata.json`).
- Setup bundle (`setup export/import/show`) now includes workflow template assets and metadata for cross-machine portability.
- Precedence model for workflow templates: repository-local override -> config storage -> built-in default fallback with explicit warnings.
- `agentvault prompt --workflow implement_issue|implement_pr` for guided repository execution flows that resolve git repo context, fetch GitHub issue/PR metadata, inject the canonical workflow template, and require structured progress checkpoints.

### Tests
- Added `internal/workflowtemplates` tests for precedence resolution, fallback warnings, import validation, and export/import metadata round-trip.
- Added prompt workflow coverage for prompt construction, git repo resolution, GitHub context loading, and missing/invalid workflow guardrails.

## [0.5.0] - 2026-03-08

### Added

#### Prompt Mode
- `-p` shortcut to enter interactive prompt mode directly from CLI root
- Prompt mode loop supports submit (`Enter`), cancel (`/cancel`), and exit (`/exit`, `quit`, `:q`) actions
- Optional prompt transcript/session metadata persistence in encrypted vault state
- Prompt transcript retention cap in vault state to avoid unbounded growth
- `prompt --validate-only` to validate provider/backend connectivity without sending a prompt
- Claude backend routing support (`anthropic`, `ollama`, `bedrock`) with backend-aware prompt execution

#### Agent Management
- `agentvault add` and `agentvault edit` support a `--backend` flag to select the Claude backend (`anthropic`, `ollama`, `bedrock`) for an agent

#### HTTP API Server
- `serve` command to start a lightweight HTTP server exposing the vault over a REST API
- Endpoints: `GET /health`, `GET /api/v1/status`, `GET /api/v1/agents`, `GET /api/v1/agents/{name}`
- Password via `AGENTVAULT_PASSWORD` env var; optional API key auth via `AGENTVAULT_SERVE_KEY`
- API keys are never exposed in any response
- Designed for integration with ForgeMind and other tools at `http://localhost:9000`

#### Agent Detection
- `detect` command to scan system for installed AI agents (Claude Code, Codex CLI, Ollama, Aider)
- `detect add` subcommand to auto-add detected agents to vault
- Detection of agent versions, paths, config directories, and settings
- Verbose mode (`--verbose`) showing detailed config information
- JSON output mode (`--json`) for scripting

#### Provider-Specific Configuration
- `ClaudeConfig` for storing Claude Code settings (plugins, keybindings, MCP servers)
- `CodexConfig` for storing Codex CLI settings (trusted projects, rules)
- `OllamaConfig` for storing Ollama settings (base URL, models)
- Functions to load/save provider configs from/to system locations

#### Unified Setup Management
- `setup export` command to create portable configuration bundles
- `setup import` command to restore configurations on new machines
- `setup show` command to preview bundle contents without importing
- `setup apply` command to push instructions to project directories
- `setup pull` command to pull provider configs from system into vault
- Encrypted bundle support with separate bundle password
- Installation guide generation in export bundles
- `--include-keys` flag for including API keys in exports
- `--apply-provider-configs` flag for applying configs during import

#### Configuration Generation
- `generate claude` command to create ~/.claude/settings.json
- `generate codex` command to create ~/.codex/config.toml and rules
- `generate env` command to create .env files with agent configurations
- `generate mcp` command to create MCP server configuration files
- `generate all` command to generate all configurations at once
- `--dry-run` flag to preview changes
- `--merge` flag to merge with existing configs
- `--no-keys` flag to exclude API keys from .env files

#### Enhanced TUI
- Multi-tab interface: Agents, Instructions, Rules, Sessions, Detected, Commands, Status
- Tab navigation with Tab/Shift+Tab and number keys (1-7)
- Search/filter functionality on Agents tab (press `/`)
- Instructions tab showing stored instruction files with preview
- Detected tab showing installed CLI agents and vault status
- Status tab showing vault info and provider config status
- Help screen (press `?`)
- Refresh functionality (press `r`)
- Improved keyboard navigation and visual feedback
- Claude backend field in agent detail and in-place backend switching (`b`) for Claude profiles

#### Vault Enhancements
- `ProviderConfigs` field for storing provider-specific settings
- `SetClaudeConfig`, `SetCodexConfig`, `SetOllamaConfig` methods
- Provider configs included in export/import operations
- Backward-compatible vault format

### Changed
- `agentvault` with no command now launches the interactive TUI by default, while explicit CLI commands/subcommands continue to behave the same.
- `-t` / `--tui` now accepts an optional target tab (`agents`, `instructions`, `rules`, `sessions`, `detected`, `commands`, `status`) and supports command-based target inference (example: `agentvault detect add -t` opens Detected tab).
- TUI view modes renamed for clarity (viewAgentList, viewAgentDetail, etc.)
- Export/import now includes provider configurations
- Improved error messages and user feedback

### Fixed
- Code formatting issues resolved with gofmt
- Test files updated for new view mode names
- Prompt runtime configuration precedence now consistently prefers local vault values over environment variables, with environment and default fallbacks applied explicitly.
- TUI agent detail now shows effective value sources (`local`, `env`, `default`) for prompt-related model/API key/base URL fields.

---

## [0.1.0] - Initial Release

### Added
- Initial project scaffolding
- Cobra CLI with all core subcommands
- Bubbletea TUI with list/detail views
- Agent data model with provider validation (claude, gemini, codex, ollama, openai, custom)
- Encrypted vault using AES-256-GCM with Argon2id key derivation
- XDG-compliant config storage (~/.config/agentvault/)
- Instructions management (pull, push, diff, set, show, remove)
- Shared configuration (system prompt, MCP servers)
- Export/import functionality
- CI/CD workflows
- Makefile with build, test, lint targets
- Cross-platform support (Linux, macOS, Windows)
