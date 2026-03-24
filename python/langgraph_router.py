#!/usr/bin/env python3
"""Optional LangGraph-based router for AgentVault.

This script reads a JSON payload on stdin with:
- prompt
- config
- candidates

It returns a routing decision on stdout. If LangGraph is installed, the
selection runs through a tiny StateGraph. If not, it falls back to the same
Python scoring path without external dependencies.
"""

from __future__ import annotations

import json
import sys
from typing import Any, Dict, List


def _provider_bias(prompt: str, candidate: Dict[str, Any]) -> int:
    prompt_lower = prompt.lower()
    provider = str(candidate.get("agent", {}).get("provider", "")).lower()
    model = str(candidate.get("target", {}).get("model", "")).lower()
    score = 0
    if provider and provider in prompt_lower:
        score += 12
    if model and model in prompt_lower:
        score += 8
    if "claude code" in prompt_lower and provider == "claude":
        score += 15
    if "codex" in prompt_lower and provider == "codex":
        score += 15
    if "ollama" in prompt_lower and str(candidate.get("target", {}).get("runner", "")) == "ollama_http":
        score += 10
    return score


def _select_candidates(state: Dict[str, Any]) -> Dict[str, Any]:
    prompt = str(state.get("prompt", ""))
    candidates = list(state.get("candidates", []))
    for candidate in candidates:
        candidate["langgraph_score"] = int(candidate.get("score", 0)) + _provider_bias(prompt, candidate)
    candidates.sort(
        key=lambda item: (
            int(item.get("langgraph_score", 0)),
            bool(item.get("target", {}).get("local", False)),
            str(item.get("agent", {}).get("name", "")),
        ),
        reverse=True,
    )
    return {"candidates": candidates}


def _render_output(state: Dict[str, Any]) -> Dict[str, Any]:
    candidates = list(state.get("candidates", []))
    if not candidates:
        raise SystemExit("no candidates provided")
    selected = candidates[0]
    fallbacks = [c["agent"]["name"] for c in candidates[1:4] if c.get("target", {}).get("supported", False)]
    reasons = list(selected.get("reasons", []))
    if selected.get("langgraph_score") != selected.get("score"):
        reasons.append("LangGraph provider/model bias adjusted the final ranking.")
    return {
        "mode": state.get("mode", "langgraph"),
        "selected_agent": selected["agent"]["name"],
        "fallbacks": fallbacks,
        "reasons": reasons,
    }


def _run_without_langgraph(payload: Dict[str, Any]) -> Dict[str, Any]:
    state = {"prompt": payload.get("prompt", ""), "candidates": payload.get("candidates", []), "mode": "python-fallback"}
    state.update(_select_candidates(state))
    return _render_output(state)


def _run_with_langgraph(payload: Dict[str, Any]) -> Dict[str, Any]:
    from langgraph.graph import END, START, StateGraph

    graph = StateGraph(dict)
    graph.add_node("select_candidates", _select_candidates)
    graph.add_edge(START, "select_candidates")
    graph.add_edge("select_candidates", END)
    app = graph.compile()
    state = app.invoke({"prompt": payload.get("prompt", ""), "candidates": payload.get("candidates", []), "mode": "langgraph"})
    return _render_output(state)


def main() -> int:
    payload = json.load(sys.stdin)
    try:
        result = _run_with_langgraph(payload)
    except Exception:
        result = _run_without_langgraph(payload)
    json.dump(result, sys.stdout)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
