# AgentVault TUI Reference

Related docs: [Docs Index](./README.md) | [CLI Reference](./cli-reference.md) | [Workflows](./workflows.md) | [Detailed Usage](./USAGE_DETAILED.md)

Launch TUI:

```bash
agentvault
# or
agentvault --tui
# or
agentvault -t
# optional target
agentvault -t rules
# inferred target from command
agentvault session list -t
```

Tabs:
1. Agents
2. Instructions
3. Rules
4. Sessions
5. Detected
6. Commands
7. Status
8. About

## Global keys

- `Tab`: next tab
- `Shift+Tab`: previous tab
- `1-8`: jump to tab
- `j/k` or `Down/Up`: move cursor
- `Enter`: open detail where available
- `Esc`: back or close modal
- `q` or `Ctrl+C`: quit
- `?`: help
- `r`: refresh vault data and detected provider state
- `Space`: open the actions menu

The help bar at the bottom and the `?` screen list the keys for the screen
you are on, including each actions-menu flow.

## Actions menu

`Space` opens the actions menu from any tab. Press an item's letter, or move
with `j/k` and press `Enter`. `Esc` returns to the screen you came from.

| Key | Action | Keys from start |
|-----|--------|-----------------|
| `a` | Add agent: name, provider, model, optional base URL | 3 (`Space a`, type, `Enter`) |
| `s` | Switch agent: pick the current agent | 3 (`Space s Enter`) |
| `p` | Prompt the current agent | 3 (`Space p`, type, `Enter`) |
| `u` | Usage: open the Status tab | 2 (`Space u`) |
| `i` | Import: form, dry-run preview, then import | 3 (`Space i Enter` to preview) |
| `x` | Export: form, then export | 3 (`Space x Enter`) |
| `:` or `;` | Run command: the `:` CLI command bridge | 2 (`Space :`) |

Add agent form: `Tab`/`Shift+Tab` move between fields, `Left`/`Right` change
the provider, `Enter` saves, `Esc` cancels. Input is checked with the same
rules as `agentvault add`. The form takes no API key; if the agent needs one, set it with
`agentvault edit NAME --api-key KEY`.

Current agent: shown under the tab bar. It lasts for the TUI session and is
not saved. In the switch list, `Enter` makes the highlighted agent current and
`x` clears it.

Prompt: with a current agent (or a vault with one agent), the Prompt Gateway
opens on the prompt step. `Enter` runs the prompt in the default workspace
(the current directory, or its repository root), shown in full as `Runs in:`. `Tab` goes to the workspace and preview steps
instead, and `Esc` goes back to agent selection. Without a current agent the
flow starts at agent selection, and the agent picked becomes current.

The `:` command entry still works everywhere; none of these flows needs it.

### Import and export

Both forms build an `agentvault` command line and run the binary; the TUI
does not import or export by itself. Each form shows the exact command it
will run, so the same options can be used on the CLI.

Export form: output file (empty: the CLI default
`<config dir>/exports/<host>-<timestamp>.avbundle`), include API keys
(on, `--include-keys=false` when off), encrypt (on, `--plain --confirm` when
off) and include memory (`--include-memory`). `Tab` moves, `Space` toggles,
`Enter` runs `agentvault export -y ...` on the terminal, where it asks for the
bundle password. A warning line says when keys or secrets go into the file.

Import form: bundle (empty: the CLI picks the newest bundle in
`<config dir>/exports`), strategy (`merge`, `replace`, `mirror`; `Left`/`Right`
cycle), on conflict (CLI decides, `backup-replace`, `keep`), project root
(adds `--apply-projects --project-root DIR`) and path maps. On the map field
type `OLD=NEW` and press `Enter` to add it; entries are listed in order and
passed as repeated `--map`. `Ctrl+D` removes the last entry. An entry without
both sides is refused.

`Enter` elsewhere on the import form runs the same command with `--dry-run`
on the terminal, so it can ask for the master and bundle passwords, then
shows its output in the TUI. From the preview, `y` or `Enter` runs the
real import on the terminal (it may ask for the bundle password or to confirm
project matches), `e` or `Esc` returns to the form. `mirror` deletes local
items the bundle does not have: the form and preview show a warning, and only
`M` runs it. After a failed preview neither runs: fix the form with `e` and
preview again. A leading `~` in a path, or in the NEW side of a map, is
expanded to the home directory.

## Tab 1: Agents

Views:
- list
- detail

Keys:
- `/`: start search mode
- `Enter`: open detail
- `d`: delete selected agent (confirmation required)

Detail view keys:
- `b`: cycle Claude backend (`anthropic` -> `ollama` -> `bedrock`; when `ollama` is selected in TUI gateway execution, the Claude agent is routed through the Ollama gateway)

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

Auto behavior:
- detects local instruction files from current project and session project dirs:
  - `AGENTS.md`
  - `CLAUDE.md`
  - `codex.md`
  - `.github/copilot-instructions.md`
- current project files are auto-synced into vault instructions

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

Live monitoring:
- provider usage/quotas are refreshed periodically
- running sessions display per-agent provider token/quota snapshots

## Tab 5: Detected

View:
- detected local agents with vault membership status

Keys:
- `a`: add selected detected agent to vault
- `Enter`: open selected detected agent detail
- `c`: connect selected detected agent to Prompt Gateway

Auto behavior:
- detected installed agents are automatically added to vault

## Tab 6: Commands

This tab has 2 operating modes.

### A) Quick command menu (default)

Keys:
- `j/k`: select command
- `Enter`: run selected command

Use this for common actions without typing.

### B) CLI command bridge

Keys:
- `:` or `;`: command entry mode
- `Enter`: run command
- `Esc`: cancel command entry

Behavior:
- you enter command without binary name
- TUI executes as `agentvault <command>`
- afterwards the TUI reads the vault file again, so what the command wrote
  shows up and is kept by the next save from the TUI; if the file was
  rewritten under another password, the TUI stops saving and asks for a
  restart

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

3. Select execution workspace
- type or edit workspace path
- `Backspace`: edit
- `Enter`: validate and continue
- `Esc`: return to prompt edit

4. Preview rewritten prompt
- `y` or `Enter`: confirm and run
- `n`: return to workspace edit
- `Esc`: return to workspace edit

5. Running
- waits for execution completion

6. Result
- shows response and token usage when available
- `s`: switch agent and start another interaction
- `e`: edit/retry prompt
- `Esc`: exit gateway

Supported providers in TUI gateway execution:
- `claude` (when backend is `ollama`, executes via Ollama gateway route)
- `codex`
- `gemini`
- `ollama`

Execution notes:
- Codex runs in agentic workspace-write mode.
- Claude runs with auto permission mode.
- Gemini runs with auto-edit approval mode.
- Gemini receives both supported API key aliases from AgentVault runtime config.
- Gateway preview shows exact execution workspace before run.
- Prompt text still does not infer repository context. Choose target workspace explicitly in gateway.

## Tab 7: Status

Read only dashboard with:
- vault path
- counts for agents/instructions/rules/roles/sessions
- provider config presence
- detected agents summary
- estimated cost from the prompt history: the total, each provider's total
  and this month's spend, and a budget warning once a provider reaches 80%
  of its monthly budget (`agentvault config pricing set PROVIDER
  --monthly-budget USD`); the same numbers as `agentvault status
  --cost-report`
- model capability matrix: one row per `agentvault capability` entry with
  its context size, a `yes` per capability column (coding, review, analysis,
  documentation, general, vision, embedding) and any other label, and the
  endpoint below it with credentials masked

The cost is read when the tab opens and every 5 seconds while it is open.

No tab specific action keys.

## Modal behavior

Delete confirmation:
- `y`: confirm
- `n` or `Esc`: cancel
