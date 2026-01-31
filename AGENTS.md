# Repository Guidelines

## Project Structure

- `cmd/`: Cobra CLI command definitions (one file per subcommand).
- `internal/agent/`: Agent data model and validation.
- `internal/vault/`: Encrypted vault CRUD operations.
- `internal/crypto/`: AES-256-GCM encryption and Argon2id key derivation.
- `internal/config/`: XDG-compliant config path resolution.
- `internal/tui/`: Bubbletea TUI application.
- `main.go`: Entry point; calls `cmd.Execute()`.

## Build, Test, and Development Commands

- Build: `make build` (produces `./agentvault` binary)
- Test: `make test` (runs `go test ./...`)
- Lint: `make lint` (gofmt check + go vet)
- Format: `make fmt`
- Clean: `make clean`
- Install: `make install` (copies to $GOPATH/bin)
- Run: `./agentvault --help`

## Coding Style

- Go: `go fmt`/`go vet` clean. Lowercase packages, no underscores.
- Use `internal/` for non-exported packages.
- Table-driven tests preferred.
- Error handling: return errors, do not panic.

## Commit Messages

- Conventional Commits: `feat:`, `fix:`, `chore:`, `docs:`, `refactor:`.
- Keep messages imperative and concise.
- Release branches: `release/X.Y.Z`.

**Strictly forbidden**: Never add `Co-Authored-By`, `Signed-off-by`, or any other trailer that attributes commits to any AI agent, model, or entity. The only person permitted to appear as author or co-author in any commit is **Nik Reljin**. This rule has no exceptions.

## CI/CD

- CI: `nikolareljin/ci-helpers` reusable workflows at `@production`.
- PR gate: lint + test + build + release tag check.
- Release: multi-target Go build on tag push (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64).
- Packaging: Homebrew, Deb, RPM, PPA workflows on tag push.

## Versioning

- `VERSION` file is the source of truth.
- Build injects version via ldflags.
- Tags match VERSION (no `v` prefix).
