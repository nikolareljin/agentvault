# AgentVault

A CLI/TUI tool for managing AI agents, their configurations, unified rules, and multi-agent sessions across machines.

## Features

- **Multi-Agent Management**: Claude, Codex, Meldbot, Openclaw, Nanoclaw, Ollama, Gemini, OpenAI, Aider, and custom providers
- **Unified Rules**: Define rules once, apply across all agents (e.g., "never mention AI model names in commits")
- **Roles System**: Assign personas like "Lead Engineer" or "Security Auditor" to agents
- **Multi-Agent Sessions**: Run multiple agents in parallel on the same project
- **Encrypted Vault**: AES-256-GCM encryption with Argon2id key derivation
- **Portable Setup**: Export/import complete configurations including sessions between machines
- **Agent Detection**: Auto-detect installed CLI agents
- **Unified Instructions**: Sync AGENTS.md, CLAUDE.md, codex.md, etc. across projects
- **Interactive TUI**: Multi-tab interface with search, filtering, and status views
- **MCP Server Support**: Configure Model Context Protocol servers per agent

## Quick Start

```bash
# 1. Initialize vault with master password
agentvault init

# 2. Detect and add installed agents
agentvault detect add

# 3. Initialize default rules and roles
agentvault rules init
agentvault roles init

# 4. Create a multi-agent session
agentvault session create my-project --dir /path/to/project

# 5. Start all agents in parallel
agentvault session start my-project

# 6. Launch TUI to manage everything
agentvault --tui
```

## Installation

### From Source

```bash
git clone https://github.com/nikolareljin/agentvault.git
cd agentvault
make build
./agentvault --help
```

### Homebrew (macOS/Linux)

```bash
brew install nikolareljin/tap/agentvault
```

## Commands Overview

### Core Commands
| Command | Description |
|---------|-------------|
| `init` | Initialize encrypted vault |
| `detect` | Detect installed AI agents |
| `detect add` | Auto-add detected agents |
| `prompt <name>` | Route prompts through AgentVault gateway with usage logging |
| `status` | Show token usage and quota status (JSON for orchestration) |
| `--tui`, `-t` | Launch interactive terminal UI |

### Agent Management
| Command | Description |
|---------|-------------|
| `add <name>` | Add new agent |
| `list` | List all agents |
| `edit <name>` | Edit agent (supports `--role`, `--disable-rules`) |
| `remove <name>` | Remove agent |
| `run <name>` | Show effective configuration |

### Unified Rules
| Command | Description |
|---------|-------------|
| `rules list` | List all rules |
| `rules init` | Add default rules |
| `rules add <name>` | Add custom rule |
| `rules enable/disable <name>` | Toggle rule |
| `rules export` | Export as markdown |

### Roles
| Command | Description |
|---------|-------------|
| `roles list` | List all roles |
| `roles init` | Add default roles |
| `roles add <name>` | Add custom role |
| `roles apply <role> <agent>` | Apply role to agent |

### Sessions (Multi-Agent)
| Command | Description |
|---------|-------------|
| `session create <name>` | Create session with agents |
| `session start <name>` | Start all agents in parallel |
| `session stop <name>` | Stop running agents |
| `session list` | List all sessions |
| `session export <name> <file>` | Export session |
| `session import <file>` | Import session |

### Instructions Sync
| Command | Description |
|---------|-------------|
| `sync to <dir>` | Generate instruction files from rules |
| `sync vault` | Update vault's stored instructions |
| `instructions pull <dir>` | Import from project |
| `instructions push <dir>` | Write to project |

### Setup Export/Import
| Command | Description |
|---------|-------------|
| `setup export <file>` | Export complete configuration |
| `setup import <file>` | Import configuration |
| `setup pull` | Pull provider configs from system |

## Provider Usage Status

AgentVault exposes provider usage and quota metadata for orchestration:

```bash
# JSON output for other apps/agents
agentvault status --json

# Non-interactive unlock for automation
AGENTVAULT_PASSWORD=... agentvault status --json

# Skip vault data and only report provider usage
agentvault status --no-vault --json
```

## Prompt Gateway

Send prompts through AgentVault instead of calling agent CLIs directly:

```bash
# direct prompt through configured agent
agentvault prompt my-codex --text "review this implementation"

# optimize prompt shape for local Ollama models
agentvault prompt my-ollama --text "build auth middleware"

# optimize for codex/copilot-style coding flows
agentvault prompt my-codex --text "refactor this endpoint" --optimize-profile codex
agentvault prompt my-copilot --text "write tests for this function" --optimize-profile copilot

# JSON output for orchestration systems
agentvault prompt my-ollama --text "summarize this design" --json
```

Prompt runs are stored in `~/.config/agentvault/prompt-history.jsonl` by default.

## Unified Rules

Rules ensure consistent behavior across all AI agents:

```bash
# Initialize default rules
agentvault rules init

# Default rules include:
# - no-model-in-commit: Never mention AI model names in commits
# - no-ai-attribution: Don't add "generated by AI" comments
# - consistent-style: Follow existing code patterns
# - minimal-changes: Make focused, minimal changes
# - no-secrets-in-code: Never hardcode secrets

# Add custom rule
agentvault rules add no-todos \
  --description "Don't leave TODO comments" \
  --content "Never leave TODO, FIXME, or HACK comments. Complete the work or create an issue." \
  --category coding \
  --priority 50

# Export rules as markdown for instruction files
agentvault rules export > RULES.md
```

## Roles System

Assign personas to agents for consistent behavior:

```bash
# Initialize default roles
agentvault roles init

# Default roles include:
# - lead-engineer: Senior technical leader
# - designer: UI/UX focus
# - security-auditor: Security focused
# - code-reviewer: Quality focused

# Apply role to agent
agentvault roles apply lead-engineer my-claude

# Or set via edit
agentvault edit my-claude --role lead-engineer
```

## Multi-Agent Sessions

Run multiple agents working together:

```bash
# Create session with specific agents
agentvault session create my-project \
  --dir /path/to/project \
  --agents claude,codex,meldbot \
  --role lead-engineer

# Start all agents in parallel
agentvault session start my-project

# Start sequentially instead
agentvault session start my-project --sequential

# Dry run to see what would start
agentvault session start my-project --dry-run

# Export session for another machine
agentvault session export my-project session.json

# Import on new machine
agentvault session import session.json
```

## Sync Instructions Across Agents

Generate consistent instruction files for all agents:

```bash
# Generate AGENTS.md, CLAUDE.md, codex.md, etc. from rules
agentvault sync to /path/to/project

# Preview what would be generated
agentvault sync preview

# Update vault's stored instructions
agentvault sync vault

# Then push to any project
agentvault instructions push /path/to/project
```

## TUI Navigation

| Key | Action |
|-----|--------|
| `Tab` / `l` | Next tab |
| `Shift+Tab` / `h` | Previous tab |
| `1-7` | Jump to tab |
| `j`/`k` | Navigate list |
| `Enter` | View details |
| `/` | Search (Agents tab) |
| `:` | Run any CLI command |
| `r` | Refresh |
| `?` | Help |
| `q` | Quit |

**Tabs**: Agents, Instructions, Rules, Sessions, Detected, Commands, Status

## Supported Agents

| Provider | CLI | Detection |
|----------|-----|-----------|
| Claude | `claude` | ✓ |
| Codex | `codex` | ✓ |
| Meldbot | `meldbot` | ✓ |
| Openclaw | `openclaw` | ✓ |
| Nanoclaw | `nanoclaw` | ✓ |
| Ollama | `ollama` | ✓ |
| Aider | `aider` | ✓ |
| Gemini | - | - |
| OpenAI | - | - |
| Custom | - | - |

## Well-Known Instruction Files

| Name | Filename |
|------|----------|
| `agents` | `AGENTS.md` |
| `claude` | `CLAUDE.md` |
| `codex` | `codex.md` |
| `meldbot` | `MELDBOT.md` |
| `openclaw` | `OPENCLAW.md` |
| `nanoclaw` | `NANOCLAW.md` |
| `copilot` | `.github/copilot-instructions.md` |
| `aider` | `.aider.conf.yml` |
| `cursor` | `.cursorrules` |
| `windsurf` | `.windsurfrules` |

## Export/Import Workflow

```bash
# On source machine: export everything
agentvault setup export my-setup.bundle --include-keys

# Optional: include provider usage/quota snapshot for orchestration
agentvault setup export my-setup.bundle --include-status

# Includes:
# - All agents and configurations
# - Unified rules
# - Roles
# - Sessions
# - Provider configs (Claude plugins, Codex rules, etc.)
# - Instructions
# - Optional status snapshot (token/quota usage metadata)

# On target machine: import
agentvault init
agentvault setup import my-setup.bundle --apply-provider-configs

# Or export/import just a session
agentvault session export my-project session.json
agentvault session import session.json
```

## Development

```bash
make build    # Build binary
make test     # Run tests
make lint     # Check formatting
make fmt      # Auto-format
```

## Security

- **Encryption**: AES-256-GCM with random nonces
- **Key Derivation**: Argon2id (64MB memory)
- **Storage**: Vault file is mode 0600
- **API Keys**: Masked in TUI, excluded from exports by default

## License

MIT
