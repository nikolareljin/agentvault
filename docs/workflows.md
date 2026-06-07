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

With `--merge`, `setup import` also restores and merges shared router settings from the bundle, so imported prompt-routing policy survives cross-machine setup replication; existing router configuration is otherwise preserved unless the current router config is empty.

## LangGraph sidecar routing

```bash
export AGENTVAULT_LANGGRAPH_ROUTER_CMD="./python/langgraph_router.py"
agentvault route --router langgraph --text "choose the best target for this coding task"
agentvault prompt --auto --router langgraph --text "implement this feature with tests"
```

The Python router is optional. If LangGraph is installed, the sidecar uses a small `StateGraph`; otherwise the script falls back to a Python-only ranking path and still returns a valid routing decision.

## Local-AI router (Ollama classification)

Sends the prompt to a local Ollama model for structured classification (complexity 1–10, task type, urgency, privacy sensitivity) before routing. Falls back silently to heuristic if Ollama is unreachable.

```bash
agentvault route --router local-ai --text "implement JWT authentication middleware"
agentvault prompt --auto --router local-ai --text "analyze security vulnerabilities in this module"

# Override model or endpoint
agentvault route --router local-ai \
  --local-ai-model llama3.2:3b \
  --local-ai-url http://localhost:11434 \
  --text "refactor this service"
```

## LLM-router mode (HTTP server)

Calls any OpenAI-compatible `/v1/chat/completions` server for intelligent routing decisions.
Compatible with llama-server, bitnet-server, Ollama, and any llm-gateway-helpers deployment.

```bash
agentvault route --router llm-router \
  --llm-router-url http://localhost:8080 \
  --text "write unit tests for the authentication module"

agentvault prompt --auto \
  --router llm-router \
  --llm-router-url http://localhost:8080 \
  --text "implement the feature from issue #42"
```

## Embedded BitNet inference (no external server)

Build once; the engine stays compiled into the binary. No server process required at runtime.

```bash
# One-time build (requires cmake + gcc, ~5 min)
make build-llama
make build-bitnet        # → ./agentvault-bitnet

# One-time model download (~400 MB)
./agentvault-bitnet routing-model download

# Route without any server
./agentvault-bitnet route \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "implement binary search in Rust"

# Execute with embedded routing
./agentvault-bitnet prompt --auto \
  --router llm-router \
  --llm-router-model-path ~/.local/share/agentvault/models/bitnet_b1_58-2B-4T.gguf \
  --text "refactor this service to use interfaces"
```

## Importance and deadline routing

```bash
# Critical + immediate → strongly prefers cloud runners with low latency
agentvault prompt --auto \
  --importance critical --deadline immediate \
  --text "production authentication service is returning 500s"

# Low + background → prefers local/low-cost targets
agentvault prompt --auto \
  --importance low --deadline background \
  --text "add docstrings to the utility module"

# Inspect the routing decision without executing
agentvault route --importance high --deadline immediate \
  --text "deploy this hotfix"
```

## Cost tracking and budget workflow

```bash
# Run prompts — cost is tracked automatically in prompt-history.jsonl
agentvault prompt my-claude --text "review this pull request"
agentvault prompt local-ollama --text "generate test cases"

# Human-readable cost report
agentvault status --cost-report

# JSON for CI/orchestration (exits non-zero if budget alerts exist)
AGENTVAULT_PASSWORD='...' agentvault status --cost-report --json | \
  jq -e '.cost.budget_alerts | length == 0'
```

## Model capability registry workflow

```bash
# Auto-discover from running endpoints
agentvault capability discover --endpoint http://localhost:11434
agentvault capability discover --endpoint http://localhost:8080

# Add with explicit context window
agentvault capability add \
  --endpoint http://localhost:11434 \
  --model llama3.1:70b \
  --context 32768 \
  --caps code,general,reasoning

# The registry augments all routing modes at scoring time
agentvault route --text "generate embeddings for this text"
```
