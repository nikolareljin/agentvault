# Testing Guide for AgentVault

This document provides comprehensive instructions for testing AgentVault functionality.

## Prerequisites

- Go 1.24+ installed
- At least one AI agent CLI installed (Claude Code, Codex CLI, or Ollama)
- A terminal that supports ANSI colors

## Building for Testing

```bash
# Standard pure-Go build (no CGo, cross-compilable)
make build

# Or build directly with Go
go build -o agentvault .

# Verify build
./agentvault version
```

### Embedded-inference build (optional — needed for llm-router with local GGUF model)

The embedded BitNet/llama.cpp engine is compiled in only when you use `make build-bitnet`.
The default `make build` binary uses a stub that falls back to heuristic routing.

```bash
# 1. Build the llama.cpp static library (one-time, ~5 min, ~2 GB build tree)
make build-llama
ls third_party/llama/lib/libllama.a   # must exist

# 2. Compile the bitnet-enabled binary
make build-bitnet
ls -lh agentvault-bitnet              # ~30–40 MB

# 3. Verify engine state
./agentvault-bitnet routing-model status
# Expected output includes:
#   embedded inference: enabled
```

## Running Automated Tests

```bash
# Run all unit tests
make test

# Run tests with verbose output
go test -v ./...

# Run tests for a specific package
go test -v ./internal/vault
go test -v ./internal/tui
go test -v ./internal/agent
go test -v ./internal/crypto
go test -v ./internal/router       # includes llm-router, balancer, scoring tests
go test -v ./internal/localllm     # stub + interface tests (no CGo required)

# Run tests with coverage
go test -cover ./...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

## Manual Testing Checklist

### 1. Vault Initialization

```bash
# Clean start (backup existing vault first if needed)
rm -rf ~/.config/agentvault

# Initialize new vault
./agentvault init
# Enter password: testpass123
# Confirm password: testpass123

# Expected: "Vault initialized at ~/.config/agentvault/vault.enc"

# Verify vault file exists
ls -la ~/.config/agentvault/vault.enc
# Expected: -rw------- (mode 0600)
```

### 2. Agent Detection

```bash
# Detect installed agents
./agentvault detect

# Expected output (varies by system):
# Detected AI Agents:
# ────────────────────────────────────────────────────────────
#   claude (claude)
#     Version:    2.x.x (Claude Code)
#     Path:       /path/to/claude
#     Config:     ~/.claude
#   codex (codex)
#     Version:    codex-cli x.x.x
#     Path:       /path/to/codex
#     Config:     ~/.codex
# ────────────────────────────────────────────────────────────

# Detect with verbose output
./agentvault detect --verbose
# Shows settings, config files, plugins, etc.

# Detect with JSON output
./agentvault detect --json
# Returns structured JSON

# Auto-add detected agents
./agentvault detect add
# Expected: "Added claude (claude)" etc.
```

### 3. Agent Management

```bash
# List agents
./agentvault list
# Enter password when prompted

# Add agent manually
./agentvault add test-agent \
  --provider openai \
  --model gpt-4 \
  --api-key sk-test-key-123

# Add agent with routing metadata (used by routing engine)
./agentvault add local-ollama \
  --provider ollama \
  --model llama3.1 \
  --base-url http://localhost:11434 \
  --route-capabilities coding,general,analysis \
  --latency-tier low \
  --cost-tier low \
  --privacy-tier local

# List with JSON output
./agentvault list --json

# List showing API keys
./agentvault list --show-keys

# Edit agent
./agentvault edit test-agent --model gpt-4-turbo

# View agent config
./agentvault run test-agent
# Shows JSON with effective configuration

# View as environment variables
./agentvault run test-agent --env
# Shows: export OPENAI_MODEL=gpt-4-turbo etc.

# Remove agent
./agentvault remove test-agent
# Type agent name to confirm, or use --force
```

### 4. Intelligent Routing

#### 4.1 Route inspection (without execution)

```bash
# Set up two test agents with different routing profiles first (see section 3)
./agentvault add cloud-codex --provider codex --model gpt-5-codex \
  --route-capabilities coding,review --latency-tier medium --cost-tier medium
./agentvault add local-ollama --provider ollama --model llama3.1 \
  --base-url http://localhost:11434 \
  --route-capabilities general,coding --latency-tier low --cost-tier low --privacy-tier local

# Inspect routing decision without running anything
./agentvault route --text "summarize this design document"
# Expected: Selected, Provider, Runner, Mode, Task class, Reasons, Fallbacks

# JSON output
./agentvault route --text "write a sorting algorithm" --json

# Force local-only
./agentvault route --text "any task" --local-only
# Expected: selects ollama-local or similar local runner

# Prefer low cost
./agentvault route --text "explain how recursion works" --prefer-low-cost

# With importance and deadline
./agentvault route --text "production incident fix" --importance critical --deadline immediate
# Expected: prefers cloud runners with low latency

./agentvault route --text "background analysis" --importance low --deadline background
# Expected: prefers local/low-cost runners
```

#### 4.2 Heuristic router (default)

```bash
./agentvault route --router heuristic --text "review this code for security issues"
# Mode: heuristic, selects by scoring (capabilities, tiers, priority)
```

#### 4.3 Local-AI router (Ollama classification)

Requires Ollama running locally. Sends prompt to a local model for structured classification
before routing. Falls back to heuristic if Ollama is unreachable.

```bash
# Use default model and URL (llama3.2 at localhost:11434)
./agentvault route --router local-ai --text "implement JWT authentication middleware"
# Mode: local-ai or local-ai-fallback, includes task class and complexity in output

# Custom Ollama endpoint and model
./agentvault route --router local-ai \
  --local-ai-model llama3.2:3b \
  --local-ai-url http://localhost:11434 \
  --text "analyze this dataset"
```

#### 4.4 LLM-router mode (HTTP server)

Requires a running OpenAI-compatible server (llama-server, bitnet-server, Ollama).

```bash
# Point to a running llama.cpp server
./agentvault route --router llm-router \
  --llm-router-url http://localhost:8080 \
  --llm-router-model llama3.2 \
  --text "refactor this service to use dependency injection"
# Mode: llm-router, includes confidence and routing factors

# With cost estimation enabled
./agentvault route --router llm-router \
  --llm-router-url http://localhost:8080 \
  --text "write tests for this module"
```

#### 4.5 LLM-router mode (embedded engine)

Requires `agentvault-bitnet` binary built with `make build-bitnet`.

```bash
# Download model first (one-time, ~400 MB)
./agentvault-bitnet routing-model download
# Default save location: ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf

# Save to a custom directory
./agentvault-bitnet routing-model download --output ~/models/

# Route using embedded engine (no server needed)
./agentvault-bitnet route \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "implement a binary search tree in Go"
# Expected: Mode=llm-router, selected agent, confidence score

# CPU thread count override
./agentvault-bitnet route \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --llm-router-context-size 512 \
  --llm-router-threads 4 \
  --text "fix the null pointer exception in the parser"

# Verify stub fallback on default binary (no CGo)
./agentvault route \
  --router llm-router \
  --llm-router-model-path /tmp/fake.gguf \
  --text "any task"
# Expected: falls back to heuristic with reason mentioning embedded inference not compiled
```

#### 4.6 Routing-model subcommand

```bash
# Show engine and model file status
./agentvault routing-model status
# Default binary output:
#   embedded inference: disabled (build with 'make build-bitnet' to enable)
#   model file:         not found (run 'agentvault routing-model download')
#   expected path:      ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf

./agentvault-bitnet routing-model status
# Bitnet binary output:
#   embedded inference: enabled
#   model file:         ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf
#   model size:         XX.XX MB
#   usage:              --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf

# Download (skip if file already exists)
./agentvault routing-model download
./agentvault routing-model download --output /tmp/models/
```

### 5. Prompt Gateway

#### 5.1 Basic prompt execution

```bash
# List configured agents first
./agentvault list

# Direct prompt through a named agent
./agentvault prompt my-claude --text "refactor this service safely"

# JSON output (disables streaming)
./agentvault prompt my-ollama --text "explain recursion" --json

# Dry run (show effective prompt without executing)
./agentvault prompt my-claude --text "review this code" --dry-run

# Prompt optimization profile
./agentvault prompt my-ollama --text "implement auth middleware" --optimize-profile ollama
./agentvault prompt my-codex --text "refactor this endpoint" --optimize-profile codex

# Timeout override
./agentvault prompt my-claude --text "big refactor task" --timeout 10m
```

#### 5.2 Automatic routing (prompt --auto)

```bash
# Let AgentVault choose the best agent
./agentvault prompt --auto --text "implement and test this Go refactor"

# With routing preferences
./agentvault prompt --auto --text "keep this local please" --prefer-local --local-only

# With importance/deadline routing signals
./agentvault prompt --auto --text "critical production fix" --importance critical --deadline immediate
./agentvault prompt --auto --text "low-priority background task" --importance low --deadline background

# With explicit router mode
./agentvault prompt --auto --router local-ai --text "analyze this dataset"
./agentvault prompt --auto --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "write a binary search implementation"
```

#### 5.3 Workflow-guided execution

Requires `git` and `gh` installed and `gh` authenticated for the target repo.

```bash
# Issue implementation workflow
./agentvault prompt my-codex \
  --workflow implement_issue \
  --repo /path/to/repo \
  --issue 16 \
  --text "Keep changes scoped to the auth module."

# PR fix workflow
./agentvault prompt my-codex \
  --workflow implement_pr \
  --repo /path/to/repo \
  --pr 28

# Dry run workflow (see generated prompt without executing)
./agentvault prompt my-codex \
  --workflow implement_issue \
  --repo /path/to/repo \
  --issue 16 \
  --dry-run

# With explicit workspace directory
./agentvault prompt my-codex \
  --workflow implement_pr \
  --repo ~/Projects/my-repo \
  --workspace ~/Projects/my-repo \
  --pr 27
```

#### 5.4 Interactive prompt mode

```bash
# Enter interactive prompt loop
./agentvault -p

# Expected behavior:
# - Prompts for agent selection when multiple agents exist
# - Submit: press Enter
# - Cancel current input: type /cancel
# - Exit loop: type /exit, quit, or :q
# - Per-message token usage printed after each response (when provider returns metadata)
# - On exit: cumulative session summary printed (input/cached/output/reasoning/total tokens)

# Validate connectivity without sending a prompt
./agentvault prompt my-claude --validate-only
```

### 6. Cost Dashboard

```bash
# Basic status (no cost)
./agentvault status --json

# With cost breakdown
./agentvault status --cost-report --json
# Expected JSON includes a "cost" section with:
#   total_usd, record_count, by_provider map, budget_alerts array

# Non-interactive (for automation)
AGENTVAULT_PASSWORD=testpass123 ./agentvault status --cost-report --json

# Human-readable (without --json)
./agentvault status --cost-report
# Shows:
#   Cost (from N prompt records):
#     Total estimated: $X.XXXXXX
#     - claude: $X.XXXXXX
#     - openai: $X.XXXXXX
#   Budget alerts (if any agent exceeds 80% of monthly budget)

# Skip vault unlock
./agentvault status --no-vault --json
```

After several `prompt` executions, `status --cost-report` aggregates cost estimates from
`~/.config/agentvault/prompt-history.jsonl`.

### 7. Model Capability Registry

```bash
# List all capability entries (initially empty)
./agentvault capability list

# Add capability entries manually
./agentvault capability add \
  --endpoint http://localhost:11434 \
  --model llama3.1:8b \
  --context 8192 \
  --caps coding,general

./agentvault capability add \
  --endpoint http://localhost:8080 \
  --model bitnet-2b \
  --context 512 \
  --caps general

# List again (should show new entries)
./agentvault capability list
# Expected columns: Endpoint, Model, Context, Capabilities

# JSON output
./agentvault capability list --json

# Auto-discover from OpenAI-compat /v1/models endpoint (llama.cpp, Ollama, etc.)
./agentvault capability discover --endpoint http://localhost:11434
# Expected: "Discovered N model(s) from http://localhost:11434"
# Adds entries with inferred capabilities from model names

# Auto-discover from /health endpoint (llm-gateway-helpers format)
./agentvault capability discover --endpoint http://localhost:8080

# Custom timeout
./agentvault capability discover --endpoint http://localhost:11434 --timeout 30s

# Remove a specific entry
./agentvault capability remove \
  --endpoint http://localhost:11434 \
  --model llama3.1:8b
# Expected: "Removed: http://localhost:11434 / llama3.1:8b"

# Verify routing uses registry: after adding capabilities,
# route with llm-router mode — the router receives capability context
./agentvault route --router llm-router \
  --llm-router-url http://localhost:8080 \
  --text "generate embeddings for this text"
# Expected: considers capability registry when scoring agents
```

### 8. Instructions Management

```bash
# Create test instruction files
mkdir -p /tmp/test-project
echo "# Test AGENTS.md" > /tmp/test-project/AGENTS.md
echo "# Test CLAUDE.md" > /tmp/test-project/CLAUDE.md

# Pull instructions from directory
./agentvault instructions pull /tmp/test-project

# List stored instructions
./agentvault instructions list
# Expected:
#   agents       -> AGENTS.md         (XX bytes, date)
#   claude       -> CLAUDE.md         (XX bytes, date)

# Show instruction content
./agentvault instructions show agents

# Set instruction from inline content
./agentvault instructions set custom --content "Custom instructions here"

# Set from file
./agentvault instructions set myfile --file /path/to/file.md

# Push instructions to new directory
mkdir -p /tmp/test-output
./agentvault instructions push /tmp/test-output
ls /tmp/test-output
# Expected: AGENTS.md, CLAUDE.md

# Diff instructions
./agentvault instructions diff /tmp/test-output
# Expected: "All instruction files are in sync."

# Modify and diff again
echo "modified" >> /tmp/test-output/AGENTS.md
./agentvault instructions diff /tmp/test-output
# Expected: Shows DIFFERS

# Remove instruction
./agentvault instructions remove custom
```

### 9. Workflow Templates

```bash
# List effective templates and their source
./agentvault templates list
# Shows: name, filename, source (built-in|config|repo-local)

# Show built-in template content
./agentvault templates show implement_issue
./agentvault templates show implement_pr
./agentvault templates show add_issue

# Initialize or refresh config-stored templates from built-in defaults
./agentvault templates refresh
./agentvault templates refresh --force   # overwrite existing

# Override in a repository: drop implement_issue.txt in repo root
echo "Custom workflow template" > /tmp/test-project/implement_issue.txt
./agentvault templates list --repo /tmp/test-project
# Expected: implement_issue source shows as "repo-local"
```

### 10. Setup Export/Import

```bash
# Pull provider configs from system
./agentvault setup pull

# Export setup to JSON
./agentvault setup export test-setup.json

# Export with API keys (vault-stored keys + env-var fallback via ANTHROPIC_API_KEY etc.)
./agentvault setup export test-setup-full.json --include-keys

# Export encrypted bundle
./agentvault setup export test-setup.bundle --encrypted
# Enter bundle password

# Include provider usage snapshot and workflow templates
./agentvault setup export test-setup.json --include-status

# Show bundle contents (includes workflow template section)
./agentvault setup show test-setup.json

# Sensitive content with confirmation gate
./agentvault setup export test-setup.json --include-secrets
# Expected: prompts for y/N confirmation before proceeding
./agentvault setup export test-setup.json --include-secrets --confirm   # non-interactive

# Import on fresh vault
rm ~/.config/agentvault/vault.enc
./agentvault init
./agentvault setup import test-setup.json

# Import with provider config application and router config merge
./agentvault setup import test-setup.json --apply-provider-configs
./agentvault setup import test-setup.json --merge --apply-provider-configs

# Apply instructions to project
./agentvault setup apply /tmp/test-project
```

### 11. Configuration Generation

```bash
# Generate Claude config
./agentvault generate claude --dry-run
# Shows what would be written

./agentvault generate claude
# Writes to ~/.claude/settings.json

# Generate Codex config
./agentvault generate codex
# Writes to ~/.codex/config.toml

# Add current directory as trusted Codex project
./agentvault generate codex --project .

# Generate environment file
./agentvault generate env
# Creates .env in current directory

./agentvault generate env .env.agents --no-keys
# Creates .env.agents without API keys

# Generate MCP config
./agentvault generate mcp

# Generate all
./agentvault generate all
```

### 12. Shared Configuration

```bash
# Show shared config
./agentvault config show

# Set shared system prompt
./agentvault config set-prompt "Always be helpful and concise."

# Add shared MCP server
./agentvault config add-mcp filesystem --command npx --args "-y,@anthropic/mcp-server-filesystem"

# Remove MCP server
./agentvault config remove-mcp filesystem
```

### 13. Rules and Roles

```bash
# Initialize default rules
./agentvault rules init
./agentvault rules list

# Add custom rule
./agentvault rules add no-todos \
  --description "Don't leave TODO comments" \
  --content "Never leave TODO, FIXME, or HACK comments." \
  --category coding \
  --priority 50

./agentvault rules disable no-todos
./agentvault rules enable no-todos
./agentvault rules export > /tmp/rules.md

# Initialize and apply roles
./agentvault roles init
./agentvault roles list
./agentvault roles apply lead-engineer my-claude
```

### 14. Sessions (Multi-Agent)

```bash
./agentvault session create my-project \
  --dir /tmp/test-project \
  --agents my-claude,local-ollama

./agentvault session list
./agentvault session show my-project
./agentvault session start my-project --dry-run
./agentvault session export my-project /tmp/session.json
./agentvault session import /tmp/session.json
./agentvault session remove my-project
```

### 15. TUI Testing

```bash
# Launch TUI
./agentvault

# Test navigation:
# - Press Tab to cycle through 8 tabs
# - Press 1-8 to jump to specific tabs directly
# - Press j/k or arrow keys to navigate lists
# - Press Enter to view details
# - Press Esc to go back
# - Press / to search (on Agents tab)
# - Press : to run any CLI command from TUI
# - Press r to refresh vault/detected/provider data
# - Press ? to show help
# - Press q to quit

# Jump directly to a specific tab
./agentvault -t detected
./agentvault -t status
./agentvault -t about

# Verify each tab shows correct content:
# Tab 1 (Agents):       List of configured agents (search with /)
# Tab 2 (Instructions): List of stored instruction files (edit with e)
# Tab 3 (Rules):        Unified rules list/details
# Tab 4 (Sessions):     Session list/details
# Tab 5 (Detected):     Installed CLI agents with vault status (add with a)
# Tab 6 (Commands):     CLI bridge (run commands with :) + Prompt Gateway (press g)
# Tab 7 (Status):       Vault path, object counts, provider config status
# Tab 8 (About):        Profile links and project info

# TUI Prompt Gateway test (tab 6, press g):
# 1. Press g to start gateway wizard
# 2. Select agent with j/k, confirm with Enter
# 3. Type prompt, press Enter to submit
# 4. Review rewritten prompt, press y to confirm or n to edit
# 5. View result; press s to switch agent, e to edit/retry, Esc to exit
```

### 16. Export/Import (Legacy Commands)

```bash
# Export to encrypted file
./agentvault export backup.enc
# Enter export password

# Export plaintext (careful!)
./agentvault export backup.json --plain

# Import encrypted
./agentvault import backup.enc

# Import plaintext
./agentvault import backup.json --plain
```

### 17. Edge Cases

```bash
# Empty vault operations
rm ~/.config/agentvault/vault.enc
./agentvault init
./agentvault list
# Expected: Shows empty list message

# Wrong password
./agentvault list
# Enter wrong password
# Expected: "wrong password or corrupted vault"

# Duplicate agent
./agentvault add test --provider claude
./agentvault add test --provider claude
# Expected: "agent 'test' already exists"

# Invalid provider
./agentvault add bad --provider invalid
# Expected: "unknown provider: invalid"

# Missing required flags
./agentvault add noname
# Expected: Error about missing provider

# prompt without agent name (common mistake)
./agentvault prompt --text "hello"
# Expected: "accepts 1 arg(s), received 0" — use --auto or provide agent name

# llm-router with no URL and no model path
./agentvault route --router llm-router --text "test"
# Expected: error mentioning --llm-router-url or --llm-router-model-path

# capability add without required flags
./agentvault capability add --endpoint http://localhost:11434
# Expected: error "required flag(s) \"model\" not set"
```

## Performance Testing

```bash
# Add many agents
for i in {1..100}; do
  ./agentvault add "agent-$i" --provider claude --model "model-$i"
done

# Time list operation
time ./agentvault list --json > /dev/null

# Time routing decision
time ./agentvault route --text "write a sort function" --json > /dev/null

# Time TUI startup
time ./agentvault --tui &
# Press q immediately
```

## Cleanup

```bash
# Remove test files
rm -f test-setup.json test-setup-full.json test-setup.bundle backup.enc backup.json
rm -f /tmp/session.json /tmp/rules.md
rm -rf /tmp/test-project /tmp/test-output /tmp/models
rm -f .env .env.agents .env.example

# Reset vault (careful - loses all data!)
rm -rf ~/.config/agentvault
```

## Troubleshooting

### Vault won't unlock
- Verify password is correct
- Check vault file isn't corrupted: `file ~/.config/agentvault/vault.enc`
- Try recreating: `rm ~/.config/agentvault/vault.enc && ./agentvault init`

### TUI display issues
- Ensure terminal supports ANSI colors
- Try different terminal emulator
- Check `TERM` environment variable

### Detection not finding agents
- Verify agents are in PATH: `which claude codex ollama`
- Check agents work: `claude --version`

### Import fails
- Verify file format matches (encrypted vs plain)
- Check password for encrypted files
- Try `./agentvault setup show <file>` to preview

### llm-router HTTP server errors
- Verify server is running: `curl http://localhost:8080/v1/models`
- Check URL (no trailing slash needed — AgentVault adds `/v1/chat/completions`)
- Use `--llm-router-timeout 60` for slow models

### Embedded inference (build-bitnet)
- `make build-llama` fails: ensure cmake 3.14+, gcc/clang, and ~2 GB disk available
- Engine loads model but crashes: check GGUF file integrity (`agentvault routing-model status`)
- Slow inference: try `--llm-router-threads $(nproc)` to use all CPU cores
- GPU offload: use `--llm-router-gpu-layers 32` (requires CUDA/ROCm build of llama.cpp)

### capability discover finds no models
- Verify endpoint responds: `curl http://localhost:11434/v1/models`
- Try `/health` fallback: `curl http://localhost:8080/health`
- Use `--timeout 30s` for slow startup

### Cost report shows $0.000000
- Cost is estimated from `prompt-history.jsonl` — requires at least one `prompt` execution
- History file: `~/.config/agentvault/prompt-history.jsonl`
- Disable with `--no-log` if you don't want history written

## CI/CD Integration

```bash
# Unlock verification (for scripts)
echo "password" | ./agentvault unlock
# Exit code 0 = success, non-zero = failure

# JSON output for parsing
./agentvault list --json | jq '.[] | .name'

# Status check without interactive unlock
AGENTVAULT_PASSWORD='...' ./agentvault status --json

# Cost check in CI (fails if budget exceeded)
AGENTVAULT_PASSWORD='...' ./agentvault status --cost-report --json | \
  jq -e '.cost.budget_alerts | length == 0'

# Environment export for other tools
eval $(./agentvault run my-agent --env)
echo $CLAUDE_MODEL

# Route decision in script
DECISION=$(./agentvault route --text "$(cat prompt.txt)" --json)
AGENT=$(echo "$DECISION" | jq -r '.selected.agent.name')
```
