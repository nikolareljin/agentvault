# Changelog

## [Unreleased]

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
