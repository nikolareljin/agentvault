# AgentVault

**NOTE:** Under development

A CLI/TUI tool for managing AI agents, their API keys, and instructions.

## Features

- Manage multiple AI agents (Claude, Gemini, Codex, Ollama, OpenAI, custom)
- Encrypted local vault (AES-256-GCM) with master password
- XDG-compliant config storage (~/.config/agentvault/)
- Interactive TUI (Charm Bubbletea)
- Export/import encrypted vault files across machines
- Cross-platform: Linux, macOS, Windows

## Installation

### From source

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

### Debian/Ubuntu

Download the `.deb` from the [releases page](https://github.com/nikolareljin/agentvault/releases).

### RPM (Fedora/RHEL)

Download the `.rpm` from the [releases page](https://github.com/nikolareljin/agentvault/releases).

## Usage

```bash
agentvault init          # Initialize vault with master password
agentvault add           # Add a new agent
agentvault list          # List all agents
agentvault edit <name>   # Edit an agent
agentvault remove <name> # Remove an agent
agentvault run <name>    # Invoke an agent
agentvault export <file> # Export vault
agentvault import <file> # Import vault
agentvault unlock        # Unlock vault
agentvault tui           # Launch interactive TUI
agentvault version       # Print version
```

## Development

```bash
make build   # Build binary
make test    # Run tests
make lint    # Check formatting and vet
make fmt     # Auto-format
make clean   # Remove binary
```

## License

MIT
