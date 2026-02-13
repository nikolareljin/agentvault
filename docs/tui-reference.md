# AgentVault TUI Reference

Related docs: [Docs Index](./README.md) | [CLI Reference](./cli-reference.md) | [Workflows](./workflows.md) | [Detailed Usage](./USAGE_DETAILED.md)

Launch TUI:

```bash
agentvault --tui
# or
agentvault -t
```

Tabs:
1. Agents
2. Instructions
3. Rules
4. Sessions
5. Detected
6. Commands
7. Status

## Global keys

- `Tab`: next tab
- `Shift+Tab`: previous tab
- `1-7`: jump to tab
- `j/k` or `Down/Up`: move cursor
- `Enter`: open detail where available
- `Esc`: back or close modal
- `q` or `Ctrl+C`: quit
- `?`: help
- `r`: refresh vault data and detected provider state

## Tab 1: Agents

Views:
- list
- detail

Keys:
- `/`: start search mode
- `Enter`: open detail
- `d`: delete selected agent (confirmation required)

Search mode keys:
- type to filter
- `Enter`: apply and exit search mode
- `Esc`: cancel search mode

## Tab 2: Instructions

Views:
- list
- detail

Keys:
- `Enter`: open detail
- `e`: edit selected instruction in external editor
- `d`: delete selected instruction

Editor selection order:
1. `$EDITOR`
2. `nano`
3. `vi`
4. `vim`

## Tab 3: Rules

Views:
- list
- detail

Keys:
- `Enter`: open detail
- `d`: delete selected rule

## Tab 4: Sessions

Views:
- list
- detail

Keys:
- `Enter`: open selected session detail

## Tab 5: Detected

View:
- detected local agents with vault membership status

Keys:
- `a`: add selected detected agent to vault

## Tab 6: Commands

This tab has 2 operating modes.

### A) CLI command bridge

Keys:
- `:` or `;`: command entry mode
- `Enter`: run command
- `Esc`: cancel command entry

Behavior:
- you enter command without binary name
- TUI executes as `agentvault <command>`

### B) Prompt Gateway flow

Keys:
- `g`: open Prompt Gateway wizard

Gateway stages and keys:

1. Select agent
- `j/k`: move
- `Enter`: continue
- `Esc`: close gateway

2. Input prompt
- type prompt text
- `Backspace`: edit
- `Enter`: rewrite prompt and continue
- `Esc`: back to agent selection

3. Preview rewritten prompt
- `y` or `Enter`: confirm and run
- `n`: return to prompt edit
- `Esc`: return to prompt edit

4. Running
- waits for execution completion

5. Result
- shows response and token usage when available
- `s`: switch agent and start another interaction
- `e`: edit/retry prompt
- `Esc`: exit gateway

Supported providers in TUI gateway execution:
- `claude`
- `codex`
- `ollama`

## Tab 7: Status

Read only dashboard with:
- vault path
- counts for agents/instructions/rules/roles/sessions
- provider config presence
- detected agents summary

No tab specific action keys.

## Modal behavior

Delete confirmation:
- `y`: confirm
- `n` or `Esc`: cancel
