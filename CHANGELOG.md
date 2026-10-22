# Changelog

## [Unreleased]

### Security

- `instructions push` and `setup apply` no longer write one project's
  referenced files into another
  (https://github.com/nikolareljin/agentvault/issues/80). `instructions pull`
  records each referenced file's source project (git remote or root
  commits, else the path); push writes it only to that project and lists
  the rest. `--from-any-project` writes them anyway. A referenced file
  stored before this release has no source project and is skipped until
  pulled again; a file stored by hand that no instruction names is written
  as before.

## [0.15.2] - 2026-10-22

### Fixed

- The `.deb` is built into `dist/` and uploaded from there. 0.15.1's `.deb`
  job failed: upload-artifact refuses the shared workflow's default
  `../*.deb`.

### Security

- `run`, `list --json`, `capability list`, `capability add`/`discover`,
  `serve` and the TUI mask base URL credentials
  (https://github.com/nikolareljin/agentvault/issues/81): a password in the
  userinfo and secret query parameters print as `***`
  (`https://user:***@host/v1`). A token used as the user name (empty or
  dummy password, or a token-shaped name) is masked whole. Request errors
  from `prompt`, `capability discover`, the TUI, the router (`--llm-router-url`,
  local-AI analysis) and `routing-model download` mask the URL they quote,
  and `capability add`/`remove` mask it in their output and errors.
  `list --show-keys` and `run --env` print the URL as stored.

## [0.15.1] - 2026-10-22

### Fixed

- The release run starts again: the `.deb` job grants `contents: write`, which
  the shared `deb-build` workflow now asks for. 0.15.0's release run stopped
  with `startup_failure` and built nothing; 0.15.1 is the first release with
  the 0.15.0 changes.

## [0.15.0] - 2026-10-21

### Added
- Import merges MCP servers one by one
  (https://github.com/nikolareljin/agentvault/issues/72): servers only
  this machine has stay, new servers are picked from a list, changed ones
  are asked about, and
  `--mcp-servers a,b|all|none` decides without a terminal. Picking a server
  is consent for its command, args and URL only: a server that reads this
  machine's environment variables, and code-loading or routing env
  variables, need a separate yes. A server with an unknown field or a
  secret-shaped value where it runs or sends is not offered.
  Changed servers show the masked difference of the changed fields and the
  bundle's definition verbatim. VS Code `mcp.servers` and Zed
  `context_servers` are merged the same way. An unknown `--mcp-servers`
  name is warned about. Without a pick nothing new is added and
  changed servers stay as they are. Covers `~/.claude.json`, JSON settings
  (`mcpServers`, `servers`) and Codex `config.toml`, whose tables are edited
  in place. Import prints per file the servers it added, replaced, left
  out or did not offer, and as `kept local` a changed server it did not
  replace; a server only this machine has is kept and not printed. JSON
  files keep this machine's key and server order. `generate mcp`
  keeps servers only the target file has.
- `import`: a work dir mapping into a hidden folder, once confirmed, is
  saved as confirmed and not asked again for the same target while every
  bundle project under it is a checkout already there with the same
  identity; anything else is asked again. Answering
  `y` or editing a work dir row in the confirm list saves the mapping.
- Bundles and the vault record projects by work dir and git identity, so
  import finds a project wherever it is checked out here. SSH and HTTPS
  remotes of one repository are one identity; owner and repo case is
  ignored on github.com and bitbucket.org only; Windows matches by remote
  only. Every match is shown and confirmed; rows can be edited. A work dir
  not found here is skipped with its projects, listed at the end (non-zero
  exit) and asked about on a terminal; mappings are saved in the vault.
  Targets inside hidden folders are confirmed one by one, even with
  `--accept-matches`. New import flags `--workdir-map`, `--accept-matches`,
  `--no-match`; new export flag `--workdir`. `--apply-projects` writes to
  the matched project without `--project-root`.
  `instructions push` applies directory patterns written for another
  checkout of the same repository.
- `import` rewrites the exporting machine's paths for this one. Its home
  directory becomes this home directory, its `--project` directory becomes
  `--project-root`, and each `--map OLD=NEW` is applied first (OLD and NEW
  must be absolute paths; NEW may start with `~/`). This covers
  file content (hook commands, MCP server paths, notes) and Claude's
  per-project memory directory names, and the provider configs that
  `--apply-provider-configs` writes. A rename that would land on another
  memory directory in the bundle is not made and is reported. Windows
  paths match in any case and with either separator (`C:/Users/x`), and a
  path moved between Windows and POSIX gets the new separators to its end.
  `--dry-run` prints every rewrite.
- `import --apply-projects --project-root DIR` writes the bundle's project
  instructions into DIR. Git internals, CI, hook directories, repository
  settings, credential files and other non-prose files are not written.
  A file already there with the same content is left as it is, mode
  included.
- JSON settings exported without `--include-secrets` travel with only their
  secret values blanked (keys with token, secret, password, key and the like,
  and everything under `env`, `headers` and `http_headers`). Import fills each
  blanked value from the file already on the machine, or leaves it out. A value
  is restored without asking only into an unchanged definition (for
  settings.json's own `env`, the rest of `env`; for a top-level key, the rest
  of the file). For a changed one import shows the old and new fields and asks
  `restore local secret into changed server? [y/N]`; without a terminal it does
  not restore it. A secret under a key that `--map` or the home rewrite renamed
  is restored too. A file with secret-shaped values under neutral keys
  (`--token x` in `args` or a command, `X-API-Key: x` header arguments, URL
  credentials, known token prefixes) is redacted whole, and so is a credential
  file by name or location (`.docker/config.json`, anything under `.aws`).
- The execute bit travels for a tool's own `hooks/` scripts and for skill
  scripts, so a hook or skill still runs after import. Import leaves a
  provider file that already matches the bundle untouched; before, it
  rewrote it as 0600 and a skill script lost its execute bit. The scripts
  travel only with `--include-secrets`; without it import names each one
  it left behind. Import writes a new or changed script only with
  `--keep-bundle-commands`.
- Import leaves out Claude permissions that let any program run without
  asking (`Bash(bash:*)`, `Bash(python3:*)`, `Bash(*)` and the like) and
  names each one. Only a plain command line is kept: literal words, no
  shell metacharacters, quotes or globs, and a command that is not an
  interpreter, wrapper (`nice`, `timeout`) or shell keyword.
  `--keep-interpreter-allows` keeps them all.
- Import leaves out what runs code that the bundle adds or changes, keeps
  this machine's value and names each one: `hooks` events, `statusLine`,
  credential helpers, plugins and marketplaces, MCP servers of any type
  unless picked (see `--mcp-servers`), code-loading `env` variables
  (`NODE_OPTIONS`, `LD_PRELOAD` and the like),
  `env` variables that route requests (`ANTHROPIC_BASE_URL`, `*PROXY*`),
  provider switches (`CLAUDE_CODE_USE_BEDROCK`), folder trust (Copilot
  `trusted_folders`, Gemini `trustedFolders.json`, `security.folderTrust`),
  JSON settings that do not parse as plain JSON, other command fields,
  trusted Codex projects, and new or changed files that are not prose or
  settings (hook scripts, extensionless files). From a Codex
  `config.toml` whose parsed `notify`, `model_providers`, `projects`,
  `approval_policy`, `sandbox_mode`, `hooks`, command fields or URL and
  endpoint keys (`chatgpt_base_url`) differ, only the MCP servers are
  applied; one that does not parse is not applied.
  `--keep-bundle-commands` keeps the hook, `statusLine` and MCP server
  commands, hook scripts and Gemini `trustedFolders.json`.
- The Codex provider config is applied as files through the same checks,
  and import no longer deletes local Codex rules. A rule name that is not
  a plain file name is left out and named by import, and refused by
  `generate codex` and `generate all` before any Codex file is written.
  Before, a name such as `../../.bashrc` wrote outside `~/.codex/rules`.
- `import --dry-run` with `--apply-provider-configs` or `--apply-projects`
  lists each file it would write, replace, keep or leave out, and writes
  nothing.
- Export skips the `.bak-YYYYMMDD-HHMMSS` backups import leaves next to
  replaced files.
- Import keeps broad allow rules such as `Bash(find:*)`, `Bash(git:*)` and
  `Bash(make:*)`, and prints one WARNING block naming each and why it can
  run other programs.
- An existing provider file, Claude project memory file or `--apply-projects`
  file that differs is backed up to `<file>.bak-YYYYMMDD-HHMMSS` and
  replaced, or kept: import asks on a terminal (with an apply-to-all
  answer), and `--on-conflict backup-replace|keep` decides without asking.
  `--strategy merge` given explicitly means keep. Backups never overwrite a
  file and are listed at the end. A JSON provider file keeps the keys only
  this machine has. The Claude provider config goes through the same checks
  instead of replacing `settings.json` and `keybindings.json` whole.
- `./dev` (and `./dev.ps1` on Windows) is the entry point for install, build,
  test, lint, format, preflight, doctor, clean and install-bin. The commands
  live in `scripts/project.sh`, and `make` targets are shims onto them.
  Without Go, `./dev install` and `./dev test` exit 1 and name the Go version
  from `go.mod`. Verbs that do not apply, such as `deploy`, exit 3.
- script-helpers as a submodule at `scripts/script-helpers`, tracking
  `production`.
- `tests/machine/install_check.sh`: installs into a scratch `GOBIN` and runs
  the installed binary.
- `export` carries the user-level setup of Gemini CLI (`~/.gemini`), the
  GitHub Copilot CLI (`~/.copilot`), Cursor and Windsurf MCP configs,
  Claude's user MCP servers from `~/.claude.json` (only `mcpServers`;
  import merges that key and keeps the rest of the file), Claude output
  styles and hooks, and Codex's global `AGENTS.md` and prompts.
- Files named by user-level instruction files (backticks, markdown links,
  `@path` imports) are carried, followed up to 10 hops, anywhere under the
  home directory. So are the scripts `settings.json` runs from `hooks` and
  `statusLine`.
- With `--project`, every file and directory the instruction files name is
  carried (up to 200 files per named directory). Before, the export only
  recorded what it could not take. Also new with `--project`: `GEMINI.md`,
  `CONVENTIONS.md`, `.github/instructions/`,
  `.github/prompts/`, `.cursor/rules/`, `.windsurf/rules/`,
  `.gemini/commands/` and `.clinerules`.
- `--include-memory` carries Claude project memory and Codex memories. It is
  off by default, and the export wizard asks each time.
- A Claude Code plugin inventory travels in the bundle. Import prints the
  `claude plugin marketplace add` / `claude plugin install` commands; it
  installs nothing.
- `GEMINI.md` is a well-known instruction file for `instructions pull` and
  `sync`.
- `instructions pull` resolves a named file from the project root when it
  is not next to the file naming it, so `.github/copilot-instructions.md`
  naming `.github/instructions/go.instructions.md` stores it. A file
  that is not UTF-8 text (an image, a UTF-16 `AGENTS.md`, NUL bytes) is
  refused and named, the rest is still pulled, and a skill missing such
  a file is named as incomplete (`export --project` carries it);
  `--strict` fails on it. Before, it was stored with its bytes replaced
  and `push` wrote that back.
- Import leaves out a bundle file over 1 MiB, the most export writes,
  and names it.
- `session import` has `--dry-run` and clears the process IDs a file
  carries (`session stop` signalled them). It adds nothing, and does not
  rewrite the vault, when the vault holds the same session under its name
  or as the `NAME-imported` copy an earlier import added; another session
  of that name becomes `NAME-imported`, then `NAME-imported-2`. Of an
  agent, rule or role named twice in the file, the first is taken. Before,
  each re-import added another `NAME-imported` copy. Bundles carry
  sessions idle, without process IDs, as `session export` does.
- `instructions import` skips an instruction equal to the vault's, and
  lists one that is not UTF-8 text as invalid. Bundle import refuses an
  instruction that is not UTF-8 text, also with `--keep-bundle-commands`,
  unless it equals the vault's copy (one stored by 0.14).
- macOS install with Homebrew: `brew install nikolareljin/tap/agentvault`.
  Each release builds one formula for arm64 and amd64 from the release
  asset digests and checks it with `brew style`, `brew test` and
  `agentvault version` before ci-helpers `homebrew-tap-publish.yml` commits
  it to the tap with the tap's deploy key (`HOMEBREW_TAP_DEPLOY_KEY`,
  `HOMEBREW_TAP_REPO` secrets).

### Changed
- Output that scripts may read changed (see Security for why):
  `instructions show`, `instructions preview`, `rules export`,
  `sync preview` and `templates show` print control characters as `?`,
  also when piped;
  add `--raw` for the exact bytes, so `rules export > FILE` now needs
  `--raw` to write the rules byte-exact. `generate env`, `generate all`
  and `run --env` leave out a value with a control character (dotenv
  also one with `'` or `${`), write the rest, and exit 1 with a count on
  stderr; the shell format unsets it. Import refuses a bundle whose names hold
  control characters, and does not add an MCP server whose command was
  blanked by export.
- A plaintext export that writes API keys or secrets now needs
  `--confirm`, and is refused without it: `export --plain` (keys are on
  by default, so also with `--include-secrets=false`), `setup export`
  with `--include-keys` or `--include-secrets` and no `--encrypted`,
  `agent export --include-key` and `session export --include-keys`.
  Scripts using `--plain` with keys must add `--confirm`. The export
  wizard asks instead.
- New dependency `github.com/BurntSushi/toml` v1.5.0: import parses Codex
  `config.toml` to compare its commands.
- `actions/checkout` and `actions/github-script` are pinned to commit SHAs.
- Go 1.26 (toolchain go1.26.9) and `golang.org/x/crypto` v0.56.0. Go 1.25
  is no longer supported upstream.
- CI runs the tests on Windows (`windows-latest`) and macOS (`macos-latest`)
  on push and pull request, next to Linux.
- A project's own settings files (`.claude/settings.json`, `.mcp.json`,
  `.gemini/settings.json`, `.codex/config.toml`, `.cursor/mcp.json`,
  `.vscode/mcp.json`) are listed in the bundle as `repository` and not
  carried, in any letter case and also when an instruction file names
  them. They travel with the repository. Before, `.claude/settings.json`
  was carried.

### Security
- Login token files (`.credentials.json`, `auth.json`, `oauth_creds.json`,
  `google_accounts.json`) are never collected, even with `--include-secrets`,
  and import never writes one from a bundle, under any spelling
  (`x/../auth.json`, `auth.json/.`, `Auth.json.`). Import takes provider
  paths only in clean form.
- `~/.copilot/config.json` travels without its login keys (`copilot_tokens`,
  `logged_in_users`, `last_logged_in_user`, any key named like a token
  except counts and limits such as `max_tokens` and `tokenLimit`),
  even with `--include-secrets`. The export lists it as carried in part and
  skips it when it is not JSON. Import strips the same keys from a bundle and
  keeps this machine's sign-in, also when the bundle spells the name
  `config.json.`, which Windows writes to `config.json`.
- Asset paths with control characters are refused, so a path printed during
  import cannot rewrite the terminal. `--apply-projects` also refuses
  segments Windows reads as another name (`.git.`, `name:stream`).
- Alternate data streams (`.env::$DATA`) name another file on Windows.
  Import and the reference closure refuse them on every OS.
- On Windows, every import write checks the name of the file it opened
  (`GetFinalPathNameByHandle`) and refuses one that differs from the name
  asked for: an 8.3 short name (`SETTIN~1.JSO`) aliasing a long-named file
  or directory. Names with `~N` (`draft~2.md`) are otherwise ordinary
  names on every OS, and a reference spelled as a short name is judged by
  its long name.
- Import on Windows refuses Windows device names (`CON`, `NUL`, `aux.md`,
  `COM1.txt`) in any path segment: Windows opens them as devices, and
  writing one can block. Other OSes write them, and export warns that
  Windows cannot.
- Codex rule names may contain `~` (`draft~2`); on Windows a rule name that
  is a device name is refused.
- Workflow template names: `con.txt` and other device names are refused on
  Windows only; export elsewhere warns that Windows cannot write them.
- A hard link inside a directory an instruction file names is refused, with
  or without `--include-secrets`, as one named directly is. Pipes, sockets
  and unreadable files there are listed as declined instead of ending the
  export.
- A pipe, socket or a name import would refuse inside a carried tool
  directory is skipped with a warning instead of ending the export.
- An asset is read through the descriptor that was checked, to at most its
  cap: a file swapped, grown past the cap or replaced by a pipe after its
  checks is not exported, and the read does not block.
- Vault and bundle files carry their key-derivation parameters in a versioned
  header (`AVK2`), and new files use Argon2id t=3, 64 MiB, 4 lanes (RFC 9106).
  Before, t=1 was fixed in the code. The header is authenticated with the
  ciphertext. Files from earlier releases still open; a vault is rewritten in
  the new format on its next save, and unlocking alone changes nothing. The
  first such save keeps the old file as `<vault>.v1.bak`. A header asking for
  more than t=8, 256 MiB or 16 lanes is refused before Argon2 runs.
  Compatibility: earlier releases cannot open AVK2 vaults or bundles; 0.14.0
  reports them as "wrong password or corrupted". The `.v1.bak` still opens
  with it.
- New vault and bundle passwords need 12 characters (was 8). Existing ones
  keep working.
- An instruction whose filename escapes the target directory, such as
  `../../.bashrc` or `/etc/profile`, is refused by `instructions set`, by
  import, and at the write in `instructions push` and `setup apply`. Before,
  a crafted bundle could make `push` write outside the project. The write
  also refuses symlinked path components below the target directory, and
  `instructions diff` reports such an entry as refused instead of reading
  the file it names. Instruction filenames are an allow list: the
  well-known instruction files, prose (`.md`, `.mdc`, `.markdown`, `.txt`,
  `.rst`) in plain directories or under `.github/instructions`,
  `.github/prompts`, `.cursor/rules`, `.windsurf/rules`, `.clinerules`,
  `.claude/agents`, `.claude/commands`, `.claude/skills` or
  `.gemini/commands` (also `.toml` there), every file in a skill directory
  (`.claude/skills/NAME/`: scripts, references, assets, templates; not
  its dot directories, dot files or `mise.toml`-style files tools load on
  their own), and files instructions refer to
  in plain directories. Any other dot directory or dot file at any depth
  (`.git`, `.vscode`, `.claude/settings.json`, `.devcontainer`,
  `pkg/.envrc`), files tools load on their own
  (`mise.toml`, `CMakeLists.txt`, `Directory.Build.props`), 8.3 short names
  and `:` streams are refused; names are checked as Windows reads them
  (case, `\`, trailing dots and spaces; a segment of only dots and
  spaces such as `.. ` is refused). A refused vault entry fails `push` and
  `apply` with the allowed forms named, before any file is written.
  Before, a bundle instruction named `.git/config` was pushed there, and its
  `core.fsmonitor` ran on the next `git status`. Import skips such an
  instruction and names it; before, import stored any filename. Under
  `mirror` this machine's entry of the same name is kept. `instructions
  pull` reports a named file it refuses and stores the rest, and
  `instructions set <name> --filename <new>` moves a stored entry.
- Import leaves out what a bundle adds or changes that runs code later,
  names it, and takes it only with `--keep-bundle-commands`: the router's
  `langgraph_command` (run by `route` and `prompt --router langgraph`),
  shared and per-agent MCP servers in the vault (written to agent configs
  by `generate`; `--mcp-servers` picks count), `.aider.conf.yml` (its
  `lint-cmd` runs) and the scripts and other non-prose files instructions
  refer to, every non-prose file in a skill directory, prose with any
  frontmatter (Claude agents, commands and skills, rules, prompts; it can
  add hooks, tools, MCP servers or a permission mode) or a shell line
  (`` !`cmd` ``, `!{cmd}`), prose that is not UTF-8 text or holds NUL
  bytes (UTF-16 with or without a byte order mark), and every
  TOML file but Codex `config.toml` (checked key by key; Gemini commands
  run `!{cmd}`). Before, all of these were taken from the bundle. Values
  already in a vault are not changed. `.clinerules/hooks/` is never
  written from a bundle. The deprecated `setup import` has no flag and
  leaves them out. `agent import` leaves out a profile's new or changed MCP
  server commands the same way and takes them with its new
  `--keep-bundle-commands`.
  `session import` does the same for the agents a session file adds,
  with its new `--keep-bundle-commands`, and `instructions import` for
  instructions that run code, with its new `--keep-bundle-commands`.
  `setup apply` names each staged file it leaves out.
- `generate env --no-keys` and the `.env.agents` of `generate all` write
  a base URL without its credential user info and secret parameters; the
  rest of the URL is kept as written. Before, a base URL such as
  `https://h/v1?api_key=x` was written as stored.
- `import`, `setup import`, `setup show`, `agent import`, `session import`
  and `instructions import` read only a regular file of at most 256 MiB,
  so `/dev/zero` or a huge file cannot exhaust memory. `instructions pull`,
  `detect`, `setup pull` and `export` read local files the same way: a
  pipe or device in place of `CLAUDE.md`, `~/.claude/settings.json` or
  `~/.codex/config.toml` is skipped instead of blocking the command.
  `status` skips a pipe named like a Codex session log, and
  `--apply-projects` refuses a staged file that is not a regular file.
- `generate env`, `generate all` and `setup apply --generate` set an
  existing env file to mode 0600 when they write keys into it. Before, a
  file created earlier as 0644 kept that mode.
- The model reaches `codex`, `claude` and `gemini` as `--model=<name>`.
  Before, a bundle model such as `--yolo` was parsed as a CLI flag.
- Prompts reach `codex` and `claude` after `--`, and `gemini` as
  `--prompt=<text>`. Before, a prompt such as `--help` sent to
  `POST /api/v1/prompt` was parsed as a CLI option.
- `serve` caps JSON request bodies at 1 MiB. At most 4 KiB of a failed
  provider response is read into an error.
- A referenced file is re-checked after it is opened: it must still be a
  regular file inside the directory and at most 256 KiB.
- Reference following (`instructions pull` and `export`) refuses credential
  files by name: `.env` and `.env.*` (templates such as `.env.example` are
  kept), keys and certificates (`.pem`, `.key`, `.p12`, ...), `.netrc`,
  `.npmrc`, `.git-credentials`, SSH keys, and anything under `.ssh`,
  `.gnupg`, `.aws`, `.kube`, `.docker` or `.azure`. Before, an instruction
  file that said "copy `.env.example` to `.env`" pulled `.env` into the vault.
  The check also runs on a reference's resolved path, so a symlink named
  `notes.md` that points at `.netrc` is refused. Directory names (`.SSH`)
  and token file names (`Auth.json`) match in any letter case. A reference
  to `~/.claude.json` is declined: that file travels as `mcpServers` only.
- A referenced file is judged by its name and by its symlink target, at user
  and at project scope. A `.local.` file named by an instruction file stays
  behind unless named with `--include-local`. Project files named by
  instructions that are not prose are redacted without `--include-secrets`.
- Outside the tools' own directories, import writes back only prose files
  (0600, never executable) and the `mcpServers` key of `~/.claude.json`. The
  merged keys are fixed in code, not read from the bundle, and a machine
  with no `~/.claude.json` yet gets only those keys. Referenced scripts
  and config files are staged for review and not written. Startup files,
  credential paths and home-root dotfiles are refused in any letter case.
- Referenced files that are not prose (scripts, config) and `~/.claude/hooks/`
  scripts are redacted without `--include-secrets`. Scripts that
  `settings.json` runs and that cannot be carried are listed as declined.
  `*.env` files are refused like `.env`. A symlinked optional file (for
  example `~/.gemini/GEMINI.md`, `~/.claude/settings.json`,
  `CONVENTIONS.md` or a project `CLAUDE.md` linked to `AGENTS.md`), or one
  that is a pipe or device, is skipped with a warning instead of failing
  the export.
  Credential and startup directories match with trailing dots and spaces
  too (`~/.aws./config`), which Windows drops.
  A home asset path with a segment ending in a dot, a colon (an NTFS
  stream) or an 8.3 short name (`.config/GIT~1/config`) is refused.
- The plugin inventory drops a credential user and password from a
  marketplace URL, so `https://token@host/repo` does not carry the token;
  a login such as `ssh://git@host/o/r` is kept, by the URL rule below.
  Only http, https, ssh, git, git+ssh and ssh+git URLs with a host are
  kept, without query or fragment; `git@host:o/r` is kept as it is; any
  other source with an `@` is dropped.
- Plugin install commands are printed only for IDs and sources that cannot
  carry shell syntax. Anything else is printed quoted, as a comment.
- A credential file by name (`.env`, `*.pem`, ...) inside a tool or rule
  directory (`~/.claude/agents/`, `.cursor/rules/`, ...), and any carried
  file with a second hard link, is redacted without `--include-secrets`. A
  hard link named `notes.md` can be `.env` under another name.
  Reference following (`instructions pull`, `--project`) refuses a file
  with a second hard link. Files in project rule directories that are
  neither prose nor TOML (`.github/instructions/token.json`) are redacted
  too.
- A user-level reference inside a tool's directory (`~/.claude`, `~/.codex`,
  `~/.gemini`, `~/.copilot`, `~/.cursor`, `~/.codeium`) is followed only into
  the setup the bundle carries, rule directories, and prose files at the
  top of that directory. Memory, session transcripts, archived sessions,
  history, backups and caches are declined. The same holds through a
  symlink: with `~/.claude` linked to `~/dotfiles/claude`, a reference to
  `~/dotfiles/claude/todos/` is declined too, and so is a link at any
  depth (`~/.claude/projects/p/memory` linked to `~/notes/mem`). Import
  does not write those paths from the home root either.
- `~/.config/github-copilot/hosts.json` and `apps.json` hold GitHub OAuth
  tokens. They are login token files like `auth.json`: never collected,
  not followed as a reference, and never written from a bundle, in any
  spelling.
- A file whose hard link count cannot be read is treated as hard-linked.
  The export warning for a hard-linked file says why it was redacted.
- `export` without `--include-secrets` no longer writes these secret values
  into the bundle: every `env` value, `apiKeyHelper` and MCP `headers` of
  `~/.claude/settings.json` (in `detected_agents` with `--detect`, and in
  `provider_configs` after `setup pull`), the `env` values and `--token x` args
  of vault MCP servers, secret-shaped agent and Ollama base URLs, agent
  provider extras, the shared router's LangGraph command and router URLs,
  and secret keys and `http_headers` of Codex and Ollama custom settings.
  A URL query parameter counts as a secret by its name under the key rules
  (`?client_secret=`, `?api-key=`, `?sig=`), and so does `X-Api-Key=x` in
  an argument. A URL carries a credential when it has a password, a
  secret-named query or fragment parameter (percent-encoded or not), or a
  user name alone on any scheme but ssh, git, git+ssh, ssh+git and sftp;
  ports and IPv6 hosts do not hide one. The user names `git`, `oauth2` and
  `x-access-token`, and an Azure DevOps organization
  (`https://org@dev.azure.com/org/...`), are kept as logins. A
  `user:password@host` without a scheme, as in a DSN
  `root:pw@tcp(db:3306)/app`, is a credential too, wherever it sits in
  the argument; `git@host:o/r`, an image digest `node:20@sha256:...` and a
  Windows path such as `C:\npm\@scope\pkg` are not.
  Before, the flag left them in. They are blanked as in
  provider files and filled on import from this machine's vault when the
  definition is unchanged: for an MCP server the same command, args and `env`
  names; for the router, all its other settings. A blank that is not
  filled is named as `Left out`, also on a machine without that value: the
  bundle lists the blanks import cannot tell from an empty value. An MCP
  server whose command is a blank that is not filled is not added, or
  keeps this machine's definition, instead of getting an empty command.
  Claude settings with a secret under a neutral key are left out, and
  import keeps this machine's. The summary keeps ordinary settings such as
  `includeCoAuthoredBy`, and the other entries of a list that holds a
  secret.
- Import fills an agent's blank API key, base URL or provider extras from
  this machine's agent only when the bundle's agent has the same provider,
  backend, base URL and provider settings, with the same extras names; a
  blank matches only a value export blanks. Otherwise the value is left
  out and named. Since 0.13.0 a bundle exported without keys could point
  an agent at another base URL and receive this machine's API key.
- A bundle exported without `--include-secrets` names its version
  `blanked_schema_version: 2.1` in place of `schema_version`, so 0.14.0 and
  earlier refuse it ("not a recognized agentvault export"). Before, 0.14.0
  imported a plain one and wrote its blanks over this machine's
  `settings.json` env, shared MCP server args and other secrets. For the
  same reason `setup export` without `--include-secrets` (its default)
  writes such a one-profile bundle instead of the 1.x layout, and
  `setup show` reads every bundle format.
- Import and `setup show` refuse a portable bundle whose version is not
  2.0 or 2.1, or whose `schema_version` and `blanked_schema_version`
  differ. Before, any version was applied as 2.0.
- `export --vault-only` with `--include-keys=false` or
  `--include-secrets=false` is refused. It wrote the whole vault anyway,
  keys and secrets in plain text, without the confirmation `--plain` needs.
- `agent export` without `--include-key`, and `session export` without
  `--include-keys`, also blank the agent's MCP server env and secret
  args, secret provider extras and a base URL with credentials; before,
  `session export` blanked only the API key. `session import` drops and
  names each blank as `agent import` does. `agent import --merge`
  fills blanks back from the local agent under the rules bundle import
  uses, the API key included, and names what it leaves out. Before, a
  profile with another base URL got this machine's key.
- A profile with such blanks is written as `blanked_schema_version: 1.1`
  with the agent under `blanked_agent` and the blanked base URL and extras
  listed in `blanks`, so 0.14.0 refuses it ("agent name is required")
  instead of writing the blanks over this machine's agent on `--merge`.
  `agent import` names a listed blank it does not fill, also for a new
  agent, and drops a blank extra. A profile with nothing blanked keeps
  the 1.0 layout. A listed blank that is not a blank base URL or extra
  of the profile's agent is refused, as is a profile of an unknown
  `schema_version` (before: a warning).
- The import report, `import --list`, the install guide, `agent import`
  and `setup show` print names, fields, paths and commands from a bundle
  or profile with control characters replaced by `?`, so a crafted file
  cannot send terminal escape sequences; so does `list`, for the name,
  provider, model and tags. A bundle or profile whose agent, MCP server,
  instruction, rule, role, session or profile name has a control
  character is refused, and `add` refuses such an agent name. An agent
  saved earlier with such a name can still be edited and removed.
- Vault fields that came from a bundle reach the terminal without control
  characters in every command that prints them: `rules`, `roles`,
  `session`, `instructions` (list, show, preview, export, diff, scan),
  `sync preview`,
  `templates` (list, show), `config show`, `run`, `generate`, `status`,
  `detect`, `setup apply`, `setup export`, `prompt --dry-run`, prompt
  mode and the TUI.
  JSON output escapes C1 control characters as `\u0080`-`\u009f`.
  Instruction, rule and prompt text keeps its newlines, tabs and CRLF,
  also when piped. New `--raw` on `instructions show`,
  `instructions preview`, `templates show`, `sync preview` and
  `rules export` prints
  content byte-exact for redirecting to a file
  (`rules export --raw > RULES.md`); `instructions export FILE` stays
  byte-exact.
  The TUI keeps only its own colours: an SGR code from a bundle is shown
  as text, so a field cannot be hidden or drawn black on black.
- `run --env` and `generate env --format shell` write values in single
  quotes, so a model or URL such as `$(cmd)` does not run when the output
  is eval'd or sourced, and a multi-line system prompt comes back
  unchanged. A value with a control character is left out with a
  warning (exit 1) and unset, so an older export of that name is not
  used instead. A name saved before control characters were refused
  cannot start a new line in the generated env file, and the provider,
  which import does not check, is cleaned like the name: `claude;cmd;`
  ran `cmd` when sourced.
- `generate env` (dotenv, the default), `generate all` and
  `setup apply --generate` write values in single quotes, so sourcing
  `.env.agents` from a shell runs nothing (0.14.0 wrote `"$(cmd)"`, which
  ran, and `setup apply` wrote a model with a newline as a second
  variable). docker compose reads them literally. A value with a single
  quote, `${` (python-dotenv expands it even in single quotes) or a
  control character is left out of the file with a warning on stderr,
  and the command exits 1. `setup apply --generate` names variables
  `PROVIDER_NAME_MODEL` like `generate all` (was `PROVIDER_MODEL`).
- `sync to`, `generate env`, `generate all` and `setup apply --generate`
  do not write through a symlink, dangling or not, at the file or (for
  `sync to` and `setup apply`) in a directory below the target. Before,
  an `AGENTS.md` or `.env` planted in a cloned repository as a link to a
  file outside it was written there.
- Import with `--apply-provider-configs` keeps this machine's
  `settings.json` or Codex `config.toml` when the bundle carries that file
  redacted whole. Before, the provider config's copy, which can be older
  than the file, replaced it, and a local `apiKeyHelper` could be lost.
  When the bundle carries the file with content, the provider config's
  copy is not applied at all. Before, it was written first, then replaced,
  which reordered keys and reported MCP servers that were not kept.
- A provider file that equals this machine's as JSON keeps this machine's
  bytes. Before, importing a bundle on the machine that made it rewrote
  `settings.json` and `~/.claude.json` with the bundle's indentation.

### Fixed
- Releases attach a `.deb` and an `.rpm` for linux/amd64 for the first
  time: `release.yml` now calls `deb.yml` and `rpm.yml`, which only ran on
  tag pushes that the auto-tag workflow never triggers, and the repo gains
  the packaging files they need (`packaging/`, `scripts/package_deb.sh`).
- `ppa.yml` runs only by hand: it no longer triggers on tag pushes, since
  the GPG and Launchpad secrets it needs are not set.
- `release-rc-pr.yml` no longer fails when the repository does not let
  Actions create pull requests (HTTP 403 "not permitted to create or approve
  pull requests"). It prints a warning with the compare link to open the
  release PR by hand; any other error, another 403 included, still fails.
- `generate mcp` writes by default to Claude Desktop's config for the OS:
  `~/Library/Application Support/Claude/` on macOS and `%APPDATA%\Claude\` on
  Windows. Before, it wrote `~/.config/claude/` everywhere, which Claude
  Desktop on macOS and Windows never reads. It replaces only the `mcpServers`
  key of an existing file, keeps the other keys in their order with their
  values as written, the file mode and, outside Windows, its owner; a UTF-8
  BOM is accepted. It writes
  through a symlink to its target, replaces the file atomically (fsync of
  the file and, outside Windows, its directory), writes a device or FIFO
  such as `/dev/stdout` in place, and refuses a file that is not a JSON
  object or a path under a symlink to a missing directory. With no home
  directory it fails instead of writing a relative path into the working
  directory.
- `import --apply-provider-configs` and `setup apply` work when the home or
  target directory sits under a symlink, as `/var` and `/tmp` do on macOS.
  Symlinks are still refused below the home or target directory. Before,
  every component from `/` was checked.
- `serve --host ::1` builds a valid listen address (`[::1]:8080`).
- `routing-model download` no longer panics when the downloaded file cannot
  be stat-ed, writes the partial file at 0600 (a stale `.part` is replaced,
  not reused with its old mode), removes it when the write or rename fails,
  and gives up after 60s without response headers.
- `instructions pull` on Windows refuses rooted references such as
  `/etc/passwd`, `C:x` and `a:b` with the reason "rooted or
  drive-qualified path on Windows". Before, they were looked up under the
  instruction directory and reported as "not found".

## [0.14.0] - 2026-10-15

### Security
- `golang.org/x/crypto` 0.55.0 (CVE-2026-56854) and `golang.org/x/text` 0.41.0
  (CVE-2026-56852), both rated HIGH; `x/sys` and `x/term` follow.

### Added
- **`instructions pull` follows references.** Files an instruction file names
  in backticks or markdown links are stored too, and the files those name, so a
  rule saying "follow `docs/review.md`" travels with that file. A reference is
  resolved relative to the file that names it. Absolute paths, paths leaving
  the directory (`..` or a symlinked parent), build and vendor trees and files
  over 256 KiB are refused and listed; a named file that is missing is listed
  too. `--strict` turns either list into a failure, and
  `--follow-references=false` turns the walk off.
- **The setup bundle carries user-level instructions**: `~/.claude/CLAUDE.md`,
  `~/.claude/agents/` and `~/.claude/commands/`, each restored to where it came
  from.
- **The setup bundle carries directory-scope settings**: a project's
  `.claude/settings.json` (redacted without `--include-secrets`) and the agent
  and command definitions beside it.
- **`.local.` files stay behind unless named.** They hold per-machine values
  and sometimes credentials. Each one is reported as declined; carry one with
  `--include-local <path>`. This applies at user and directory scope.
- **An export records what it declined, and `import` prints it**: files left
  out by policy and files an instruction names that do not exist, listed
  separately. Every policy entry is printed; missing files are capped at five
  with a count.
- **Custom workflow templates, in plain text or Markdown.** Any `<name>.txt` or
  `<name>.md` in the config templates directory is a template: `templates list`
  and `templates show` include it, a repository-root file of the same name
  overrides it, and export/import carry it. Built-in keys accept `.md` too.
  Markdown front matter `description` shows in `templates list`. A template is
  a regular file up to 256 KiB. See [docs/templates.md](docs/templates.md).
- **A stored template older than the built-in prints a warning** with both
  versions and the command to update it; an edited copy keeps winning.

### Changed
- **The built-in workflow templates are short and generic** (`builtin-3.0`):
  inputs, ordered steps and the expected output. `implement_issue` names a
  branch `release/X.Y.Z` by default and follows the repository's own naming
  when it has one. `add_issue` describes both `TODO.txt` (`[TODO]` markers) and
  `TODO.md` (one `## ` section per item). A copy stored by an earlier release
  keeps working and prints the version warning; run
  `agentvault templates refresh --force` to take the new text.

## [0.13.0] - 2026-09-01

### Added
- **`agentvault export` is now the single export entry point.** Running it with no arguments
  starts an interactive wizard whose every prompt defaults to "include it", so pressing Enter
  through it writes a complete, encrypted bundle to
  `<config dir>/exports/<host>-<timestamp>.avbundle`. `-y` takes the same defaults without asking.
  When stdin is not a terminal the wizard is skipped and the defaults apply.
- **Multi-profile bundles (schema 2.0).** Every agentvault config directory on a machine that
  holds a `vault.enc` becomes a named profile inside one bundle. Discovery covers the active
  config dir, `~/.config/agentvault`, `~/.agentvault`, sibling `agentvault*` directories, and
  every path in the new `AGENTVAULT_CONFIG_DIRS` variable. Each profile's vault is unlocked
  separately, trying `AGENTVAULT_PASSWORD` before prompting.
- **`agentvault import --strategy merge|replace|mirror`.** `merge` (default) keeps existing vault
  values, `replace` lets the bundle win on collisions, and `mirror` makes the vault match the
  bundle exactly by also deleting local-only items. `mirror` prompts for confirmation on a
  terminal and requires `--confirm` otherwise.
- **Import ergonomics**: `--list` prints a bundle's profiles without touching the vault,
  `--dry-run` reports every change it would make, `--profile NAME|all` selects profiles, and
  omitting the file argument picks the newest export in the default export directory.
- **Model capability registry travels with bundles.** `SetupBundle.model_capabilities` is
  populated on export and reconciled on import; new `Vault.SetCapabilities` replaces the list
  wholesale for `replace` and `mirror`.
- **Non-interactive credentials**: `AGENTVAULT_EXPORT_PASSWORD` and `AGENTVAULT_IMPORT_PASSWORD`
  supply the bundle password, and `openVault` now honors `AGENTVAULT_PASSWORD` like `serve` does,
  so the whole export/import cycle can run without a terminal.

### Changed
- `agentvault export` defaults flipped to a full export: API keys, secret-bearing provider files,
  sessions, workflow templates, provider home files, skill assets and detected-agent information
  are all included, and the bundle is encrypted, unless a flag says otherwise. A quota status
  snapshot stays opt-in (`--include-status`) because it is a reading, not a setting.
- `agentvault import` detects the payload format automatically. Portable bundles (2.x),
  `setup export` bundles (1.x) and legacy vault exports are all accepted; `--plain` is deprecated
  and ignored.
- Imported sessions are normalized: they arrive idle with cleared PIDs, a session that already
  exists by name keeps its local ID, and an active-session pointer that no longer resolves is
  cleared. Under `replace` and `mirror`, an agent whose bundle entry carries no API key keeps the
  key already in the vault.
- `setup export` and `setup import` are deprecated in favor of `export` and `import`. They keep
  working and keep reading and writing the schema 1.x bundle, and now share their collection and
  merge implementation with the new commands; `setup import --merge` maps onto `--strategy replace`.
- The old vault-only export format is still writable with `agentvault export --vault-only` and
  still readable by `agentvault import`.
- Provider pricing rows now travel with a bundle and are reconciled by provider plus model
  pattern, so a cost-report configuration replicates along with everything else.
- Encryption is decided by `--encrypt` / `--plain` and the wizard answer only. The output
  file's extension never changes it, so the same command always produces the same kind of file.

### Fixed
- An import no longer rewrites vault sections it did not change. Session config in particular
  was written on every import, and `Vault.SetSessions` force-sets `parallel_limit_set`, so an
  import carrying no session data silently marked an unset parallel limit as explicit.
- `openVault` and per-profile unlock now fail with an actionable message when a password is
  needed and stdin is not a terminal, instead of surfacing a low-level terminal-read error
  that hid the real remedy.
- `setup import` refuses a portable bundle holding more than one profile instead of quietly
  folding every source-machine profile into the current vault. It has no profile selection,
  so the message points at `agentvault import --profile`.
- Re-running an import that changes nothing is now a true no-op: items whose bundle value
  already matches the vault are reported as skipped rather than rewritten and re-timestamped.
- Profile discovery also finds hidden `~/.agentvault*` directories.
- Docs stated `AGENTVAULT_CONFIG_DIRS` is `:`-separated; it uses the OS path list separator.
- The plaintext-with-secrets confirmation told `export` users to add `--encrypted`, a flag
  only the deprecated `setup export` has. It now names each caller's own flag.
- Docs described sibling profile discovery as `agentvault-*`; the match is `agentvault*`.
- Instructions derived from asset overrides were stamped with the current time on import, so
  a `replace` or `mirror` re-import of an unchanged bundle rewrote them every run. They now
  carry the bundle's own creation time, which keeps a repeat import a true no-op.
- The `mirror` confirmation listed only some of what it deletes. It now names provider
  configs, pricing rows and model capability entries too, and says the shared system prompt
  and router config are cleared when the bundle does not carry them.
- `export --help` and the discovery comment said sibling directories are `agentvault-*`;
  the match is `agentvault*`, hidden `~/.agentvault*` included.
- `--strategy mirror` with more than one selected profile is refused. Profiles were applied
  in sequence to the same vault, so each one deleted what the previous added and the result
  matched only the last profile, while the confirmation implied the union of all of them.
- Profile name deduplication reserves names case-insensitively, matching how selection
  resolves them, and skips a suffix that is already taken, so `Work` and `work`, or a
  directory literally named `agentvault-work-2`, can no longer shadow another profile.

### Security
- Stored prompt sessions are excluded from exported bundles. Their entries hold prompt and
  response text, which has no place in a file meant to be copied between machines. Import
  leaves the target machine's own prompt history untouched, including under `mirror`.
- `gosec` G101 findings on the password *environment variable name* constants are annotated
  rather than renamed; the values are variable names, not credentials.

## [0.12.0] - 2026-06-06

### Added
- **`llm-router` routing mode** (#36): new fourth routing mode that calls a local llama.cpp or
  bitnet.cpp inference server (`/v1/chat/completions`) to make intelligent, cost-aware routing
  decisions. Falls back to heuristic when the server is unreachable and `--allow-fallbacks` is set;
  otherwise returns an error. New `--allow-fallbacks` flag enables graceful degradation for both
  `llm-router` and `local-ai` modes.
  Flags: `--router llm-router --llm-router-url URL --llm-router-model NAME --llm-router-timeout N`.
  Compatible with any model produced by the finetorch → shrink-llm GGUF pipeline.
- **`internal/router/llm_router.go`**: token estimator, system-prompt builder, OpenAI-compat HTTP
  client, `AnalyzeWithLLMRouter`, `enrichIntentFromLLMDecision`. Follows `local_ai.go` patterns.
- **`internal/router/balancer.go`**: `Balancer` with circuit breaker (3 consecutive failures →
  60 s cooldown) and EWMA latency tracking (`avg = avg×0.8 + sample×0.2`). `PickBest` prefers
  the LLM-chosen agent, falls back to highest-scored healthy candidate.
- **Cost projection dashboard** (#39): `ProviderPricing` struct + `DefaultPricing()` with
  well-known public rates for Claude, OpenAI, Gemini, Codex, Bedrock, and Ollama. Cost computed
  per-execution via `ComputeCostUSD` (model-pattern aware, most-specific-match wins).
  `PromptRecord.EstimatedCostUSD` logged to history. `agentvault status --cost-report` shows
  per-provider breakdown and fires budget alerts when spend exceeds 80% of `MonthlyBudgetUSD`.
- **Model capability registry** (#38): `ModelCapabilityEntry` vault CRUD (`AddCapability`,
  `RemoveCapability`, `ListCapabilities`). New `agentvault capability` subcommand with
  `list`, `add`, `remove`, and `discover` (auto-populates from `/v1/models` or `/health`).
  Registry augments agent `RouteConfig.Capabilities` at routing time for all modes.
- **`RouterConfig` fields**: `LLMRouterURL`, `LLMRouterModel`, `LLMRouterTimeoutSecs` (default 30),
  `LLMRouterEnableCostEst` added to `RouterConfig`; all merged via `mergeRouterConfig`.
- **`Request.ModelCapabilities`**: vault capability registry threaded through all routing modes
  (`heuristic`, `local-ai`, `llm-router`, `langgraph`).
- **Embedded BitNet/llama.cpp inference engine**: `llm-router` mode now supports in-process
  GGUF inference with no external server required. Build with `make build-bitnet` (CGo +
  llama.cpp static library). `--llm-router-model-path PATH` points to any GGUF model file.
  Falls back to heuristic routing when the embedded engine is not compiled in.
- **`internal/localllm` package**: `Engine` interface + CGo implementation (`-tags localllm`)
  using llama.cpp's stable C API; pure-Go stub (`!localllm`) for default cross-compilable builds.
- **`scripts/build-llama.sh`**: one-time cmake build of `ggerganov/llama.cpp` into
  `third_party/llama/` (gitignored). Re-running is a no-op once the library exists.
- **`Makefile` targets**: `make build-llama` (compile static library) and `make build-bitnet`
  (output: `agentvault-bitnet`).
- **`RouterConfig` fields**: `LLMRouterModelPath`, `LLMRouterContextSize` (default 512),
  `LLMRouterThreads`, `LLMRouterGPULayers` — all wired through `mergeRouterConfig` and
  exposed as CLI flags on `route` and `prompt`.
- **`agentvault routing-model`** subcommand: `status` (engine state + model file info) and
  `download` (stream BitNet-b1.58-2B-4T.gguf from Hugging Face with progress display).

### Changed
- `RouterConfig.Validate()` now accepts `"llm-router"` as a valid mode.
- `agentvault route --router` help text updated to include `llm-router`.
- `vaultData` schema extended with `model_capabilities` field (backward-compatible, `omitempty`).
- `SharedConfig` extended with `pricing` field (backward-compatible, `omitempty`).
- `agentvault status` now includes a `cost` section in JSON output.
- `agentvault prompt` token output line now shows `est. cost: $X.XXXXXX` when cost is non-zero.

## [0.11.0] - 2026-04-30

### Added
- **Agent profile schema** (`ProviderMeta`): `Agent` now carries optional `provider_meta`
  with auth mode, Bedrock region/ARN, Copilot org, Gemini project/location, AWS profile,
  and Ollama keep-alive. All fields are `omitempty`; existing vaults are fully backward-compatible.
  Closes #4.
- **`agentvault agent export / import`**: single-agent round-trip portability via JSON or YAML.
  Flags: `--format json|yaml`, `--include-key`, `--dry-run`, `--merge`. Schema version `1.0`
  with full `ValidateProviderMeta` checks on import. Closes #4.
- **`copilot` and `bedrock` providers**: recognized in `ValidProviders()` and
  `ProviderInstructionMap` (`copilot` → `.github/copilot-instructions.md` + `AGENTS.md`;
  `bedrock` → `AGENTS.md`).
- **Instruction scopes**: `InstructionFile` gains `scope` (`global`|`directory`|`local`) and
  `directory_pattern` (glob) fields. `global` is the default; all existing instructions
  are unaffected. Scope precedence: local > directory > global. Closes #15.
- **`agentvault instructions set --scope / --directory-pattern`**: store instructions at a
  specific scope. Directory-scoped instructions also require `--directory-pattern <glob>`.
- **`agentvault instructions preview [name]`**: show the effective merged instruction for a
  working directory (`--dir <path>`, default cwd). Use `--all` to list all resolved
  instructions. Reflects scope precedence. Closes #15.
- **`agentvault instructions export [file]`**: export the full instruction hierarchy with scope
  metadata as JSON or YAML. Filter by `--scope`.
- **`agentvault instructions import [file]`**: import instructions with conflict reporting,
  `--merge`, and `--dry-run`.
- **Import conflict reporting**: `agentvault import` now prints an instruction conflict section
  when incoming instructions collide by name at the same scope (existing wins).

### Changed
- `Vault.ImportData` returns `(imported int, skippedAgents []string, invalidInstructions []string, conflicts []agent.InstructionConflict, err error)`.
- `agentvault instructions list` shows Scope and DirectoryPattern columns when any
  stored instruction has a non-empty scope.
- `agentvault sync to` resolves effective instructions per the current working directory
  before generating content, so directory-scoped overrides take effect.
- `agentvault sync preview` gains `--dir <path>` flag for testing scope resolution.
- TUI instruction detail view shows Scope and DirectoryPattern when set.

## [0.10.0] - 2026-04-28

### Added
- **Session token summary in prompt mode**: `agentvault -p` now prints a cumulative token summary on exit showing total input, cached-input, output, reasoning, and total tokens across all messages in the session. Per-message token counts were already shown; this adds the session-level aggregate. The summary is also persisted to vault state via a new `total_token_usage` field on `PromptSession`. Closes #6.
- **Env-var API key export**: `agentvault setup export --include-keys` now also captures API keys that were set via environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, and Gemini fallback `GOOGLE_API_KEY`) when the vault-stored key for an agent is empty. This preserves credentials that were configured through the shell environment rather than stored directly in the vault. Closes #8.
- **Confirmation gate for plaintext sensitive export**: `agentvault setup export --include-secrets` without `--encrypted` now requires explicit confirmation — either an interactive `y/N` prompt or the new `--confirm` flag for scripted/CI use. Previously only a warning was printed; the export proceeded unconditionally. Closes #8.
- **Per-agent key status in export summary**: `agentvault setup export` output now includes a per-agent key status table showing `[vault key included]`, `[env key included]`, `[no key needed]`, `[no key found]`, or `[redacted]` for each exported agent, with `[no key found]` possible when `--include-keys` is requested but no key is available.

### Changed
- `internal/agent.PromptSession` gained a `TotalTokenUsage *PromptTokenUsage` field (JSON: `total_token_usage`) for aggregate session token data. The field is omitempty and fully backwards-compatible.
- `agentvault setup export --include-keys` flag description updated to mention env-var key resolution.

## [0.9.0] - 2026-04-22

### Added
- **Live streaming output**: `agentvault prompt` now streams CLI agent subprocess output (Claude, Codex, Gemini) live to the terminal via `io.MultiWriter`. Claude and Gemini omit `--output-format json` in stream mode so tool calls, file edits, and in-progress actions are visible as they happen. Streaming is automatically disabled in `--json` mode or when stdout is not a terminal; use `--no-stream` to force buffered mode.
- **Importance and deadline routing flags**: New `--importance low|medium|high|critical` and `--deadline immediate|normal|background` flags on both `agentvault prompt` and `agentvault route`. These feed directly into routing scoring — `critical` importance penalizes local targets and strongly prefers cloud runners; `immediate` deadline boosts low-latency agents; `background` deadline favors low-cost targets.
- **Local-AI router mode**: New `--router local-ai` mode sends the prompt to a local Ollama instance for structured classification (complexity 1–10, task type, urgency, privacy sensitivity, tool need) before routing. The result enriches intent detection and scoring. Gracefully falls back to heuristic with mode `local-ai-fallback` if Ollama is unreachable. Configurable via `--local-ai-model` and `--local-ai-url`.
- **Routing transparency**: Routing decisions now print the selected agent, runner, mode, task class, and top reasons before execution. `agentvault route` output now includes Mode, Importance, and Deadline fields.

### Changed
- `RouterConfig.WithDefaults` no longer forces `prefer-local` when `importance` or `deadline` is explicitly set, since those signals already express routing intent.

## [0.8.1] - 2026-04-17

### Fixed
- Codex prompt execution now automatically adds `--skip-git-repo-check` when AgentVault is launched from a directory outside any Git worktree, which fixes TUI and `prompt` failures from trusted non-repository paths such as a projects directory that is not itself a repository.
- Codex Prompt Gateway and `agentvault prompt` now launch Codex in agentic workspace-write mode instead of a bare one-shot exec, avoiding read-only chat-style behavior when edits are expected.
- Claude and Gemini prompt execution now use provider-specific agentic approval modes in both Prompt Gateway and `agentvault prompt`, instead of plain read/respond execution flags.
- Prompt Gateway now shows a live running indicator with elapsed time and agent context while waiting for provider completion, instead of a static "Running prompt..." screen.

## [0.8.0] - 2026-03-24

### Added
- `agentvault setup export` now supports `--agent`, `--project`, and `--include-secrets` for portable single-agent bundles with project-local instructions, workflow templates, and skill assets.
- Setup bundles now carry explicit `provider_files`, `project_files`, `instruction_overrides`, and `skill_assets` sections with relocatable path metadata and redaction markers for sensitive content.
- Added an intelligent prompt router with new `agentvault route` inspection command and `agentvault prompt --auto` execution path.
- Added per-agent routing metadata for capabilities, latency/cost/privacy tiers, and routing priority.
- Added optional Python LangGraph sidecar support via `python/langgraph_router.py` and `AGENTVAULT_LANGGRAPH_ROUTER_CMD`.

### Changed
- `agentvault setup show` now summarizes portable asset sections and reports sensitive or redacted bundle content.
- Prompt execution now resolves provider-agnostic runner targets before execution, including OpenAI HTTP support alongside existing Ollama, Codex, and Claude paths.

## [0.6.0] - 2026-03-14

### Added
- Expanded built-in workflow templates to use the current `2.0` issue/PR implementation bodies.
- Upgraded the built-in `add_issue` template to generate TODO entries with explicit input/output contracts, deterministic ID allocation, and embedded reusable `implement_issue` / `implement_pr` checklists.

## [0.5.2] - 2026-03-11

### Added
- TUI About tab with direct profile links for GitHub and LinkedIn.
- `--tui about` target support and updated tab navigation/help text for the new eighth tab.
- `templates` command group with `list`, `show`, and `refresh` for workflow templates (`implement_issue.txt`, `implement_pr.txt`, `add_issue.txt`).
- New workflow template storage under AgentVault config (`~/.config/agentvault/templates/`) with metadata (`metadata.json`).
- Setup bundle (`setup export/import/show`) now includes workflow template assets and metadata for cross-machine portability.
- Precedence model for workflow templates: repository-local override -> config storage -> built-in default fallback with explicit warnings.
- `agentvault prompt --workflow implement_issue|implement_pr` for guided repository execution flows that resolve git repo context, fetch GitHub issue/PR metadata, inject the canonical workflow template, and require structured progress checkpoints.

### Tests
- Added `internal/workflowtemplates` tests for precedence resolution, fallback warnings, import validation, and export/import metadata round-trip.
- Added prompt workflow coverage for prompt construction, git repo resolution, GitHub context loading, and missing/invalid workflow guardrails.

## [0.5.0] - 2026-03-08

### Added

#### Prompt Mode
- `-p` shortcut to enter interactive prompt mode directly from CLI root
- Prompt mode loop supports submit (`Enter`), cancel (`/cancel`), and exit (`/exit`, `quit`, `:q`) actions
- Optional prompt transcript/session metadata persistence in encrypted vault state
- Prompt transcript retention cap in vault state to avoid unbounded growth
- `prompt --validate-only` to validate provider/backend connectivity without sending a prompt
- Claude backend routing support (`anthropic`, `ollama`, `bedrock`) with backend-aware prompt execution

#### Agent Management
- `agentvault add` and `agentvault edit` support a `--backend` flag to select the Claude backend (`anthropic`, `ollama`, `bedrock`) for an agent

#### HTTP API Server
- `serve` command to start a lightweight HTTP server exposing the vault over a REST API
- Endpoints: `GET /health`, `GET /api/v1/status`, `GET /api/v1/agents`, `GET /api/v1/agents/{name}`
- Password via `AGENTVAULT_PASSWORD` env var; optional API key auth via `AGENTVAULT_SERVE_KEY`
- API keys are never exposed in any response
- Designed for integration with ForgeMind and other tools at `http://localhost:9000`

#### Agent Detection
- `detect` command to scan system for installed AI agents (Claude Code, Codex CLI, Ollama, Aider)
- `detect add` subcommand to auto-add detected agents to vault
- Detection of agent versions, paths, config directories, and settings
- Verbose mode (`--verbose`) showing detailed config information
- JSON output mode (`--json`) for scripting

#### Provider-Specific Configuration
- `ClaudeConfig` for storing Claude Code settings (plugins, keybindings, MCP servers)
- `CodexConfig` for storing Codex CLI settings (trusted projects, rules)
- `OllamaConfig` for storing Ollama settings (base URL, models)
- Functions to load/save provider configs from/to system locations

#### Unified Setup Management
- `setup export` command to create portable configuration bundles
- `setup import` command to restore configurations on new machines
- `setup show` command to preview bundle contents without importing
- `setup apply` command to push instructions to project directories
- `setup pull` command to pull provider configs from system into vault
- Encrypted bundle support with separate bundle password
- Installation guide generation in export bundles
- `--include-keys` flag for including API keys in exports
- `--apply-provider-configs` flag for applying configs during import

#### Configuration Generation
- `generate claude` command to create ~/.claude/settings.json
- `generate codex` command to create ~/.codex/config.toml and rules
- `generate env` command to create .env files with agent configurations
- `generate mcp` command to create MCP server configuration files
- `generate all` command to generate all configurations at once
- `--dry-run` flag to preview changes
- `--merge` flag to merge with existing configs
- `--no-keys` flag to exclude API keys from .env files

#### Enhanced TUI
- Multi-tab interface: Agents, Instructions, Rules, Sessions, Detected, Commands, Status
- Tab navigation with Tab/Shift+Tab and number keys (1-7)
- Search/filter functionality on Agents tab (press `/`)
- Instructions tab showing stored instruction files with preview
- Detected tab showing installed CLI agents and vault status
- Status tab showing vault info and provider config status
- Help screen (press `?`)
- Refresh functionality (press `r`)
- Improved keyboard navigation and visual feedback
- Claude backend field in agent detail and in-place backend switching (`b`) for Claude profiles

#### Vault Enhancements
- `ProviderConfigs` field for storing provider-specific settings
- `SetClaudeConfig`, `SetCodexConfig`, `SetOllamaConfig` methods
- Provider configs included in export/import operations
- Backward-compatible vault format

### Changed
- `agentvault` with no command now launches the interactive TUI by default, while explicit CLI commands/subcommands continue to behave the same.
- `-t` / `--tui` now accepts an optional target tab (`agents`, `instructions`, `rules`, `sessions`, `detected`, `commands`, `status`) and supports command-based target inference (example: `agentvault detect add -t` opens Detected tab).
- TUI view modes renamed for clarity (viewAgentList, viewAgentDetail, etc.)
- Export/import now includes provider configurations
- Improved error messages and user feedback

### Fixed
- Code formatting issues resolved with gofmt
- Test files updated for new view mode names
- Prompt runtime configuration precedence now consistently prefers local vault values over environment variables, with environment and default fallbacks applied explicitly.
- TUI agent detail now shows effective value sources (`local`, `env`, `default`) for prompt-related model/API key/base URL fields.

---

## [0.1.0] - Initial Release

### Added
- Initial project scaffolding
- Cobra CLI with all core subcommands
- Bubbletea TUI with list/detail views
- Agent data model with provider validation (claude, gemini, codex, ollama, openai, custom)
- Encrypted vault using AES-256-GCM with Argon2id key derivation
- XDG-compliant config storage (~/.config/agentvault/)
- Instructions management (pull, push, diff, set, show, remove)
- Shared configuration (system prompt, MCP servers)
- Export/import functionality
- CI/CD workflows
- Makefile with build, test, lint targets
- Cross-platform support (Linux, macOS, Windows)
