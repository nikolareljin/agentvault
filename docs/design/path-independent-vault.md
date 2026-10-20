# Machine-independent project paths

Status: implemented (`internal/gitid`, `internal/locations`, export, import,
`instructions pull`/`push`). Owner decisions of 2026-10-20 are folded in.

## Problem

A bundle names paths on the machine that exported it:

| Where | Field | Example |
|---|---|---|
| `SetupBundle` | `source_home`, `source_project` | `/home/alex`, `/home/alex/Projects/app` |
| provider files | Claude memory dir key in `logical_path` | `projects/-home-alex-Projects-app/...` |
| `ProviderConfigs.Codex.TrustedProjects` | map key | `/home/alex/Projects/app` |
| file content | hook commands, MCP paths, notes | `/home/alex/bin/guard.sh` |
| `ProjectFiles` | `project_relative_path` | `AGENTS.md` (already relative) |

Import (`cmd/import_remap.go`) rewrites by prefix: each `--map OLD=NEW`, then
`source_project -> --project-root`, then `source_home -> this home`. That
covers one project per import. A project at `~/Projects/app` on one machine
and `~/src/app` on the other is not found unless the user writes a `--map`.

## Data model

Added to `SetupBundle` as `locations` (optional, `omitempty`):

```
WorkDir { id, label, home_rel, abs }
Project { id, work_dir, rel, abs, git_remote, root_commits[], namespace, aliases[] }
```

The same structure is stored in the vault (`locations`), so `instructions
push` uses the same identities as import. `aliases` (vault only, never
exported) are other paths the repository was known by, such as its path on
the machine a bundle came from.

- `WorkDir.id`: stable within one bundle (`w1`, `w2`). `home_rel` is the path
  relative to `source_home` (`Projects`), `.` for home itself, empty when
  outside home. `abs` is the
  old absolute path, kept as the fallback.
- `Project.rel`: path relative to its work dir (`app`, `client/web`).
- `git_remote`: `gitid.NormalizeRemote` output, `host[:port]/owner/repo`.
  SSH, scp-like and HTTPS forms of one remote give one value
  (`git@host:o/r.git`, `ssh://git@host[:22]/o/r`, `https://[user@]host/o/r[.git][/]`).
  Owner and repo are lower-cased on github.com and bitbucket.org only; on
  every other host path case is significant. Azure DevOps SSH
  (`v3/org/proj/repo`) and HTTPS (`org/proj/_git/repo`) forms are not
  unified: they are two identities and simply do not match.
- `root_commits`: sorted root commit hashes; used when there is no remote.
- `namespace`: the remote path minus the repo name (`owner`, `group/sub`).

## Capture on export

- Projects: `--project` and the Codex trusted projects, merged with what the
  vault already records (from earlier imports and `instructions pull`).
- Work dirs: any `--workdir DIR` given; otherwise each project's parent.
- Per project: `gitid.Identify`. The remote is read by parsing the config
  file as text (handles `.git` files for worktrees and submodules,
  `commondir`); git is never run for it.
- Root commits need `git rev-list --max-parents=0 HEAD`. Parsing loose and
  packed objects in Go is the only exec-free way and is too much code for one
  hash, so git runs, hardened: `--git-dir`, `core.hooksPath=/dev/null`
  (`NUL` on Windows), `core.fsmonitor=false`, `protocol.allow=never`,
  `remote.origin.promisor=false`, env `GIT_NO_LAZY_FETCH=1`,
  `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`,
  `GIT_TERMINAL_PROMPT=0`, `GIT_COMMON_DIR` set to the checked common dir,
  other `GIT_*` removed (so `GIT_DIR`, `GIT_CONFIG_COUNT`/`KEY`/`VALUE`,
  `GIT_EXEC_PATH` cannot steer it), 5 s timeout, 1 s `WaitDelay` so a child
  holding the pipe cannot outlive the kill, stdout capped at 1 MiB. git is
  found on `PATH`; os/exec refuses a git in the current directory or a
  relative `PATH` entry (`exec.ErrDot`), so a repo cannot ship its own
  `git.exe`. `commondir` is parsed as git does
  (trailing CR/LF only, relative to the git dir) and all paths are resolved
  through symlinks first. Git runs only when the work tree, git dir, common
  dir, `config` and `config.worktree` are all owned by the current user
  (Unix uid). On Windows git is never run: matching there uses the remote
  only (root commits empty, `RootSkipped` set).
  A `config` or `config.worktree` with an `include` or `includeIf`
  section also skips git (`RootSkipped` set): git would load files the
  owner check cannot name. Ownership is checked after symlinks are
  resolved; a race that swaps a path between check and exec needs write
  access to a directory this user owns, which is out of scope.
  Unborn HEAD, or a HEAD ref outside `refs/`, is detected
  from files and skips git. No git binary: project keeps path only.
- Normalized remotes never hold `@` or `:` in a path segment; scp-like
  `user:token@host:path` drops everything up to the last `@` before the
  first slash.
- Refused (no remote recorded), because each could give two repos one
  identity or put a non-host in it: control characters anywhere, a
  backslash, `%2F`/`%5C` in a URL path (`a%2Fb/c` would equal `a/b/c`), `..`
  segments, a host that is not an IP or an ASCII name of `[a-z0-9._-]`
  starting with neither `-` nor `.` (covers `-oProxyCommand=...`, homoglyph
  and non-punycode IDN hosts), and transport forms `ext::`, `fd::`, `file://`.
  An IDN host is accepted only in its `xn--` form; the Unicode spelling of
  the same host is refused rather than unified.
- The remote reader is a text parser, not git. Known differences, all of
  which make a match miss (fall back to `abs`), never point elsewhere,
  because both machines read remotes the same way:
  - `include`/`includeIf` files are not followed for the remote;
  - `url.<base>.insteadOf` and `pushurl` are not applied: the identity is
    the fetch URL as written in the repo config;
  - legacy `[remote.origin]` headers, escapes in a quoted subsection name,
    and remotes defined only in `config.worktree` are not read.
- A project that no longer exists on disk is recorded with `abs` only.

## Matching on import

Order: work dirs first, then projects inside the matched work dirs, then
projects anywhere under the candidate roots.

1. Work dir: a `--workdir-map OLD=NEW` first, else same `home_rel` under this
   home, else a mapping saved in the vault by an earlier import (shown in
   the confirmation list like a computed match), else same `label` directly
   under home. A work dir that holds a project with an identity and is still
   not found is never guessed and does not stop the import: it is recorded,
   its projects are left out, and everything else is imported. On a
   terminal the user is then asked for each such work dir, with the three
   closest top-level home folders offered, and only that work dir's
   projects are matched again. A mapping the user gives (an answer or
   `--workdir-map`) is saved in the vault. What is still missing is listed
   at the end with the `--workdir-map OLD=<path here>` line to add, and the
   import exits non-zero.
   A target with a hidden folder (name starting with `.`) below home,
   work dir or project, needs its own confirmation per entry (y, or a
   different path), even when it exists and even with `--accept-matches`.
   `--workdir-map` naming that exact target is the confirmation. Without a
   terminal an unconfirmed hidden target is treated as missing.
2. Candidates for a project: git checkouts found by a bounded scan (depth 3,
   skip `node_modules`, `vendor`, hidden dirs, never follow symlinks) of the
   matched work dirs and of home. When every bundle project has a remote,
   the scan never runs git.
3. Score each candidate:

| Signal | Score |
|---|---|
| same `git_remote` (exact; host already lower-cased) | 100 |
| shares a root commit, remote missing on one or both sides | 80 |
| shares a root commit, remotes differ (fork) | 30 |
| same `rel` under the matched work dir | +20 |
| same directory name | +10 |

   Both sides have root commits and share none: not the same repository,
   score 0 whatever the remote says.
4. Best score >= 80 and alone at that score: match. Two or more at the top
   score: ambiguous. Below 80: no match; fall back to `abs` and the home
   rewrite, as today.
5. Every computed match, unique or not, is a proposal (see Security). All of
   them, work dirs first, are shown as one numbered list,
   `bundle path -> local path` with score and reason. The user enters a row
   number to change that row's path (an existing absolute directory, `~/`
   allowed; a letter picks a candidate of an ambiguous row; `-` skips the
   row), an empty line to accept the list, `q` to abort the import. End of
   input aborts.
6. Without a terminal: with `--accept-matches` unique matches are applied
   and ambiguous ones skipped with a `--map` hint; without it the list is
   printed, no computed path is used and no project files are written.
7. Accepted project matches are recorded in the vault (local path, bundle
   path as alias). `--apply-projects` without `--project-root` writes into
   the accepted match of the bundle's `--project`.

## Flags

- `--map OLD=NEW`: unchanged; always wins over a computed match.
- `--workdir-map OLD=NEW`: map a whole work dir; projects under it match by
  `rel` and identity.
- `--no-match`: old behaviour, literal paths only.
- `--accept-matches`: apply unique computed matches without a terminal.
- export `--workdir DIR`: record a work dir explicitly.
- `--dry-run`: prints each match with its score and reason, asks nothing.

## How remap consumes it

Each project match becomes a `pathRemap{From: abs, To: matched}` and each work
dir match one more, inserted before the `source_home` rule.
`buildPathRemaps` already sorts by prefix length, so a project wins over its
work dir and the work dir over home. Content, Claude memory keys
(`remapLogicalPath` via `claudeProjectKey`) and Codex `TrustedProjects` keys
(`remapProviderConfigs`) are then rewritten by the existing code, no new path.
A memory dir key that would collide is still refused and reported.

## Compatibility

- Bundles without `locations` import exactly as today.
- Old importers ignore the unknown field; paths still carry `abs`.
- `source_home` and `source_project` stay written.

## Security

- Bundle data only selects among directories found on this machine; it is
  never joined into a write path. `rel` and `home_rel` are rejected if
  absolute or containing `..`.
- Identities in a bundle are claims, not proof. Remotes are public and root
  commits of a public repo are too, so a hostile bundle can name the
  user's own dotfiles or work repo and score 100. A computed match
  therefore never writes on its own:
  - on a terminal, the import lists every computed match (bundle path ->
    local path, score, reason) and asks once before using any of them;
  - without a terminal, computed matches are reported with ready
    `--map OLD=NEW` lines and skipped unless `--accept-matches` is given;
  - `--map`/`--workdir-map` targets are the user's own choice and need no
    confirmation.
- A match target must also be under this home, or named in
  `--map`/`--workdir-map`.
- A bundle `abs` must be an absolute path below the filesystem root with no
  control characters before it becomes a row or a rewrite; others are
  dropped with a warning. Bundle strings are printed with control
  characters replaced.
- Bundle `aliases` are ignored. Import records the bundle `abs` as the one
  alias, unless it is this home or above it, or equals, contains or sits
  below any path or alias of a project with a different identity (known to
  the vault or accepted earlier in the same import).
- Bundle paths keep their own OS form (`/home/a` stays forward-slashed on
  Windows), so they still match the text they rewrite.
- The scan never follows symlinks out of the scanned root and never runs git
  in a directory it did not find under that root.
- Remote URLs are stored normalized only, so tokens in `https://tok@host`
  never reach the bundle.

## Tests

- `internal/gitid`: URL table, config parsing, real repos (worktree,
  submodule, packed refs, detached HEAD, unborn, orphan roots).
- Matcher: pure function over fake candidate lists; ties, forks, renamed work
  dirs, missing projects, `--map` precedence.
- Import e2e: export from temp home A with `~/Projects/app`, import into temp
  home B with `~/src/app`; memory key and Codex trust land on the new path.
- Old bundle fixture imports unchanged.

## Decisions (2026-10-20)

1. No work dir match: never guess, never abort. Record it, skip its
   projects, import the rest, list it at the end (non-zero exit). On a
   terminal ask for each (closest home folders offered) and match that work
   dir again; save the answer and every `--workdir-map` in the vault.
7. Targets in hidden folders below home are confirmed per entry;
   `--accept-matches` does not cover them, `--workdir-map` to that exact
   target does.
2. Azure DevOps remote forms are not unified now.
3. Owner/repo case is folded only for github.com and bitbucket.org.
4. Work dirs and projects are stored in the vault too.
5. Windows matches by remote only; SSH and HTTPS forms of a remote are one
   identity on every OS (`NormalizeRemote` is OS-independent).
6. Every match is confirmed, unique ones included, in one editable list;
   `--accept-matches` for non-interactive imports, which otherwise print
   the list and write no project files.
