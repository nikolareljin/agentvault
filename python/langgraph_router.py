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
            -int(item.get("langgraph_score", 0)),
            not bool(item.get("target", {}).get("local", False)),
            str(item.get("agent", {}).get("name", "")),
        ),
    )
    return {"candidates": candidates}


def _render_output(state: Dict[str, Any]) -> Dict[str, Any]:
    candidates = list(state.get("candidates", []))
    if not candidates:
        raise SystemExit("no candidates provided")
    config = state.get("config", {})
    local_only = bool(config.get("local_only", False))
    allowed_candidates: List[Dict[str, Any]] = []
    for candidate in candidates:
        target = candidate.get("target", {})
        if not target.get("supported", False):
            continue
        if local_only and not target.get("local", False):
            continue
        allowed_candidates.append(candidate)
    if not allowed_candidates:
        raise SystemExit("no allowed candidates available")
    selected = allowed_candidates[0]
    selected_agent_name = selected.get("agent", {}).get("name")
    if not selected_agent_name:
        raise SystemExit("invalid candidate payload: missing agent.name for selected candidate")
    fallbacks: List[str] = []
    for candidate in allowed_candidates[1:4]:
        agent_payload = candidate.get("agent")
        if not isinstance(agent_payload, dict) or not agent_payload.get("name"):
            raise SystemExit("invalid candidate payload: missing agent.name for fallback candidate")
        fallbacks.append(str(agent_payload["name"]))
    reasons = list(selected.get("reasons", []))
    if selected.get("langgraph_score") != selected.get("score"):
        reasons.append("LangGraph provider/model bias adjusted the final ranking.")
    return {
        "mode": state.get("mode", "langgraph"),
        "selected_agent": selected_agent_name,
        "fallbacks": fallbacks,
        "reasons": reasons,
    }


def _run_without_langgraph(payload: Dict[str, Any]) -> Dict[str, Any]:
    state = {
        "prompt": payload.get("prompt", ""),
        "config": payload.get("config", {}),
        "candidates": payload.get("candidates", []),
        "mode": "python-fallback",
    }
    state.update(_select_candidates(state))
    return _render_output(state)


def _run_with_langgraph(payload: Dict[str, Any]) -> Dict[str, Any]:
    from langgraph.graph import END, START, StateGraph

    graph = StateGraph(dict)
    graph.add_node("select_candidates", _select_candidates)
    graph.add_edge(START, "select_candidates")
    graph.add_edge("select_candidates", END)
    app = graph.compile()
    state = app.invoke(
        {
            "prompt": payload.get("prompt", ""),
            "config": payload.get("config", {}),
            "candidates": payload.get("candidates", []),
            "mode": "langgraph",
        }
    )
    return _render_output(state)


def main() -> int:
    try:
        payload = json.load(sys.stdin)
    except Exception as exc:
        print(f"Failed to read JSON payload from stdin: {exc}", file=sys.stderr)
        return 1
    try:
        result = _run_with_langgraph(payload)
    except (ImportError, ModuleNotFoundError):
        result = _run_without_langgraph(payload)
    except Exception as exc:
        print(
            f"LangGraph routing failed, falling back to pure-Python router: {exc}",
            file=sys.stderr,
        )
        result = _run_without_langgraph(payload)
    json.dump(result, sys.stdout)
    sys.stdout.write("\n")
    return 0


def _self_test() -> None:
    state = {
        "prompt": "general request",
        "candidates": [
            {"score": 10, "agent": {"name": "zeta", "provider": "codex"}, "target": {"local": True, "supported": True}},
            {"score": 10, "agent": {"name": "alpha", "provider": "codex"}, "target": {"local": True, "supported": True}},
        ],
    }
    ranked = _select_candidates(state)["candidates"]
    if ranked[0].get("agent", {}).get("name") != "alpha":
        raise SystemExit("langgraph candidate ordering should break ties by agent name ascending")


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--self-test":
        _self_test()
        raise SystemExit(0)
    raise SystemExit(main())
