# Machine-independent project paths

Status: design, for review. Only `internal/gitid` is implemented.

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
Project { id, work_dir, rel, abs, git_remote, root_commits[], namespace }
```

- `WorkDir.id`: stable within one bundle (`w1`, `w2`). `home_rel` is the path
  relative to `source_home` (`Projects`), empty when outside home. `abs` is the
  old absolute path, kept as the fallback.
- `Project.rel`: path relative to its work dir (`app`, `client/web`).
- `git_remote`: `gitid.NormalizeRemote` output, `host[:port]/owner/repo`.
- `root_commits`: sorted root commit hashes; used when there is no remote.
- `namespace`: the remote path minus the repo name (`owner`, `group/sub`).

## Capture on export

- Work dirs: the parent of every path that names a project (`--project`,
  Claude memory keys decoded against existing dirs, Codex trusted projects,
  directory-scoped instruction patterns) plus any `--workdir DIR` given.
- Per project: `gitid.Identify`. The remote comes from `.git/config` read as
  a file (handles `.git` files for worktrees and submodules, `commondir`).
  Root commits need `git rev-list --max-parents=0 HEAD`, run with
  `--git-dir`, `core.fsmonitor=false`, no prompt, 5 s timeout. Unborn HEAD is
  detected from files and skips git. No git binary: project keeps path only.
- A project that no longer exists on disk is recorded with `abs` only.

## Matching on import

Order: work dirs first, then projects inside the matched work dirs, then
projects anywhere under the candidate roots.

1. Work dir: same `home_rel` under this home if it exists (score 100), else a
   `--workdir-map OLD=NEW`, else same `label` under home (50). Ties ask.
2. Candidates for a project: git checkouts found by a bounded scan (depth 3,
   skip `node_modules`, `vendor`, hidden dirs) of the matched work dirs, then
   of home's top-level dirs if not found.
3. Score each candidate:

| Signal | Score |
|---|---|
| same `git_remote` (case-folded) | 100 |
| shares a root commit, remote missing on one or both sides | 80 |
| shares a root commit, remotes differ (fork) | 30 |
| same `rel` under the matched work dir | +20 |
| same directory name | +10 |

4. Best score >= 80 and alone at that score: match. Two or more at the top
   score: ambiguous. Below 80: no match; fall back to `abs` and the home
   rewrite, as today.
5. Ambiguous on a terminal: list candidates, ask. Without a terminal: print
   each ambiguity with a ready `--map OLD=NEW` line and skip that project.

## Flags

- `--map OLD=NEW`: unchanged; always wins over a computed match.
- `--workdir-map OLD=NEW`: map a whole work dir; projects under it match by
  `rel` and identity.
- `--no-match`: old behaviour, literal paths only.
- `--dry-run`: prints each match with its score and reason.

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
- A match target must be under this home, or the user confirms it (terminal)
  or names it in `--map`/`--workdir-map`.
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

## Open questions

1. Scan scope when no work dir matches: home top level only, or ask?
2. Azure DevOps ssh (`v3/org/proj/repo`) vs https (`org/proj/_git/repo`):
   normalize to one form now or later?
3. Should `git_remote` comparison fold path case for every host, or only for
   github.com, bitbucket.org (GitLab paths are case-sensitive)?
4. Record work dirs in the vault itself (for `instructions push`), or only in
   export bundles for now?
