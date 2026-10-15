# Workflow templates

A workflow template is a short checklist that tells an agent how to do one kind
of task. `agentvault prompt --workflow` sends it with the task, and
`agentvault templates` lists, shows and initializes the templates.

A template is a plain file you can edit: plain text (`<name>.txt`) or Markdown
(`<name>.md`), whichever you prefer. Claude and Codex read both.

agentvault ships three generic templates. They are a starting point: edit them,
replace them per repository, or add your own.

| Key | File | What it describes |
|---|---|---|
| `implement_issue` | `implement_issue.txt` | Implement an issue: branch, change, tests, docs, pull request |
| `implement_pr` | `implement_pr.txt` | Address pull request review comments |
| `add_issue` | `add_issue.txt` | Add items to a `TODO.txt` or `TODO.md` task list |

`agentvault prompt --workflow implement_issue|implement_pr` uses the first two.
`agentvault templates show <key>` prints whichever copy is in effect.

## Where a template comes from

For each template, the first of these that exists wins:

1. `<repo>/<name>.txt` or `<repo>/<name>.md` at the repository root (`--repo`, default: the current directory).
2. `<config dir>/templates/<name>.txt` or `.md`: your stored copy (default `~/.config/agentvault/templates/`; honors `XDG_CONFIG_HOME` and `--config`).
3. The built-in.

When both `<name>.txt` and `<name>.md` exist in one place, the `.txt` file is
used and a warning names the other. `agentvault templates list` shows each
template with its source: `repo-local`, `config` or `built-in`.

## Change a template

```bash
agentvault templates refresh                 # write the built-ins into config storage
$EDITOR ~/.config/agentvault/templates/implement_issue.txt
agentvault templates show implement_issue    # check what is now in effect
```

To use Markdown instead, save your version as `implement_issue.md` and delete
the `.txt` copy; `templates refresh` leaves a `.md` copy alone and does not write
a `.txt` beside it.

`refresh` never overwrites a stored file. To go back to the built-in, run
`agentvault templates refresh --force`, which overwrites all stored built-in
templates, a lone `.md` copy included.

When a release changes a built-in, a stored copy written from an older built-in
prints a warning naming both versions. Keep your copy (edit it, and the warning
stays as a reminder), or take the new one with `refresh --force`.

### Branch names

The built-in `implement_issue` names a new branch `release/X.Y.Z` with the next
semantic version by default, and follows the repository's own naming when it has
one. Nothing enforces that: change step 2 in your copy, or in one repository's
`implement_issue.txt`, to whatever your branches look like.

## Use a different template in one repository

Put a file with the template's name at the repository root, for example
`./implement_pr.md`. It applies to that repository only, and overrides your
stored copy and the built-in.

## Add your own template

Any other `<name>.txt` or `<name>.md` in `<config dir>/templates/` is a template
too. The name uses lowercase letters, digits, `-` and `_`. Its key is the name
with `-` turned into `_`, so `release-notes.md` has the key `release_notes`.

A Markdown template may start with front matter (a `.txt` template is sent as
is). `description` is shown by
`templates list`; the agent is sent the text after the front matter.

```markdown
---
description: Write release notes from the changelog
---

# Release notes

1. Read the changelog section for the version.
2. Group the entries: added, changed, fixed.
3. One line per entry, user-facing wording.
```

```bash
agentvault templates list
agentvault templates show release_notes
```

A file of the same name at a repository root overrides it there, as for the
built-ins. A symlink to a template file counts as the file. Files with other names (`Notes.txt`, `notes.json`) are ignored.

## The TODO formats (`add_issue`)

`TODO.txt` keeps items between `[TODO]` and `[/TODO]` markers:

```
[TODO]
ID: 001
Title: Add a login rate limit
Description: Limit failed logins per account.
Implementation Details:
- Count failures per account for 15 minutes.
Acceptance Criteria:
- The sixth failed login within 15 minutes is refused.
[/TODO]
```

`TODO.md` has one `## <title>` section per item:

```markdown
## Add a login rate limit

ID: 001

Limit failed logins per account.

Implementation Details:
- Count failures per account for 15 minutes.

Acceptance Criteria:
- The sixth failed login within 15 minutes is refused.
```

Both forms can be turned into GitHub issues by a tool that reads them, such as
`lantern todo issues` from git-lantern.

## Write a good template

- State inputs, ordered steps, and the output you want back.
- Keep it general: the repository's own docs (`CONTRIBUTING.md`, `AGENTS.md`)
  hold its specific rules, and the agent reads those too.
- Never put secrets, tokens or private paths in a template: it is plain text,
  and it travels in exports.

## Moving to another machine

`agentvault export` includes every stored template, built-in and custom, `.txt`
and `.md`, and `agentvault import` writes them back into config storage under
the same names. An imported file replaces a stored copy of that template under
the other extension. Repository-local files travel with their repository, not in the
bundle.
