# AgentVault Workflows

## Daily workflow through TUI gateway

```bash
agentvault -t
```

Then:
1. go to Commands tab
2. press `g`
3. select agent
4. type prompt
5. review rewritten prompt
6. confirm run
7. press `s` to switch agent for next interaction

## Add agent and use prompt gateway from CLI

```bash
agentvault add my-codex --provider codex --model gpt-5
agentvault prompt my-codex --text "review this endpoint" --optimize-profile codex
```

## Local Ollama optimization workflow

```bash
agentvault add local-ollama --provider ollama --model llama3.1 --base-url http://localhost:11434
agentvault prompt local-ollama --text "implement jwt auth middleware" --optimize-profile ollama
```

## Copilot style optimization for custom agent

```bash
agentvault add my-copilot --provider custom --model copilot-chat
agentvault prompt my-copilot --text "write tests for parser edge cases" --optimize-profile copilot
```

## Status checks for orchestration

```bash
agentvault status --json
agentvault status --no-vault --json
AGENTVAULT_PASSWORD='***' agentvault status --json
```

## Cross machine sync

Source machine:

```bash
agentvault setup export team.bundle --encrypted --include-status
```

Target machine:

```bash
agentvault init
agentvault setup import team.bundle --merge --apply-provider-configs
```
