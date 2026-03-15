# Testing Guide for AgentVault

This document provides comprehensive instructions for testing AgentVault functionality.

## Prerequisites

- Go 1.24+ installed
- At least one AI agent CLI installed (Claude Code, Codex CLI, or Ollama)
- A terminal that supports ANSI colors

## Building for Testing

```bash
# Build the binary
make build

# Or build directly with Go
go build -o agentvault .

# Verify build
./agentvault version
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

### 4. Instructions Management

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

### 5. Setup Export/Import

```bash
# Pull provider configs from system
./agentvault setup pull
# Expected: Pulls Claude plugins, Codex trusted projects, etc.

# Export setup to JSON
./agentvault setup export test-setup.json

# Export with API keys (careful!)
./agentvault setup export test-setup-full.json --include-keys

# Export encrypted bundle
./agentvault setup export test-setup.bundle --encrypted
# Enter bundle password

# Show bundle contents
./agentvault setup show test-setup.json
# Shows agents, instructions, provider configs

# Import on fresh vault
rm ~/.config/agentvault/vault.enc
./agentvault init
./agentvault setup import test-setup.json

# Import with provider config application
./agentvault setup import test-setup.json --apply-provider-configs

# Apply instructions to project
./agentvault setup apply /tmp/test-project
```

### 6. Configuration Generation

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
# Creates MCP server configuration

# Generate all
./agentvault generate all
```

### 7. Shared Configuration

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

### 8. TUI Testing

```bash
# Launch TUI
./agentvault --tui

# Test navigation:
# - Press Tab to cycle through tabs (Agents, Instructions, Rules, Sessions, Detected, Commands, Status, About)
# - Press 1-8 to jump to specific tabs
# - Press j/k or arrow keys to navigate lists
# - Press Enter to view details
# - Press Esc to go back
# - Press / to search (on Agents tab)
# - Press : to run any CLI command from TUI
# - Press r to refresh
# - Press ? to show help
# - Press q to quit

# Verify each tab shows correct content:
# Tab 1 (Agents): List of configured agents
# Tab 2 (Instructions): List of stored instruction files
# Tab 3 (Rules): Unified rules list/details
# Tab 4 (Sessions): Session list/details
# Tab 5 (Detected): Installed CLI agents with vault status
# Tab 6 (Commands): CLI parity bridge (run any command)
# Tab 7 (Status): Vault path, counts, provider config status
# Tab 8 (About): Profile links and project info
```

### 9. Export/Import (Legacy Commands)

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

### 10. Edge Cases

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
```

## Performance Testing

```bash
# Add many agents
for i in {1..100}; do
  ./agentvault add "agent-$i" --provider claude --model "model-$i"
done

# Time list operation
time ./agentvault list --json > /dev/null

# Time TUI startup
time ./agentvault --tui &
# Press q immediately
```

## Cleanup

```bash
# Remove test files
rm -f test-setup.json test-setup-full.json test-setup.bundle backup.enc backup.json
rm -rf /tmp/test-project /tmp/test-output
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
- Check TERM environment variable

### Detection not finding agents
- Verify agents are in PATH: `which claude codex ollama`
- Check agents work: `claude --version`

### Import fails
- Verify file format matches (encrypted vs plain)
- Check password for encrypted files
- Try `./agentvault setup show <file>` to preview

## CI/CD Integration

```bash
# Unlock verification (for scripts)
echo "password" | ./agentvault unlock
# Exit code 0 = success, non-zero = failure

# JSON output for parsing
./agentvault list --json | jq '.[] | .name'

# Environment export for other tools
eval $(./agentvault run my-agent --env)
echo $CLAUDE_MODEL
```
