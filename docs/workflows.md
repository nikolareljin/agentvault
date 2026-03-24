# AgentVault Workflows

Related docs: [Docs Index](./README.md) | [CLI Reference](./cli-reference.md) | [TUI Reference](./tui-reference.md) | [Detailed Usage](./USAGE_DETAILED.md)

## Daily workflow through TUI gateway

```bash
agentvault
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
agentvault add my-codex --provider codex --model gpt-5 --route-capabilities coding,review --latency-tier medium --cost-tier medium
agentvault prompt my-codex --text "review this endpoint" --optimize-profile codex
```

## Automatic routing workflow

```bash
agentvault add local-ollama --provider ollama --model llama3.1 --base-url http://localhost:11434 \
  --route-capabilities general,coding,analysis --latency-tier low --cost-tier low --privacy-tier local
agentvault add my-codex --provider codex --model gpt-5-codex \
  --route-capabilities coding,review,analysis --latency-tier medium --cost-tier medium

agentvault route --text "summarize this design and keep everything local"
agentvault prompt --auto --text "implement and test this Go refactor"
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

## Guided issue implementation workflow

```bash
agentvault prompt my-codex \
  --workflow implement_issue \
  --repo /path/to/repo \
  --issue 16 \
  --text "Prefer the existing command structure and keep docs concise."
```

This resolves the repo root, loads `implement_issue.txt` with template precedence rules, fetches the issue title/body from GitHub, and sends one structured prompt with required progress checkpoints.
It requires both `git` and `gh` on `PATH`, and `gh` must be authenticated for the target repository.

## Guided PR fix workflow

```bash
agentvault prompt my-codex \
  --workflow implement_pr \
  --repo /path/to/repo \
  --pr 28
```

This resolves the repo context, loads `implement_pr.txt`, fetches PR metadata from GitHub, and tells the agent to report progress through the same auditable checkpoint sequence.
It requires both `git` and `gh` on `PATH`, and `gh` must be authenticated for the target repository.

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

## LangGraph sidecar routing

```bash
export AGENTVAULT_LANGGRAPH_ROUTER_CMD="python3 ./python/langgraph_router.py"
agentvault route --router langgraph --text "choose the best target for this coding task"
agentvault prompt --auto --router langgraph --text "implement this feature with tests"
```

The Python router is optional. If LangGraph is installed, the sidecar uses a small `StateGraph`; otherwise the script falls back to a Python-only ranking path and still returns a valid routing decision.
