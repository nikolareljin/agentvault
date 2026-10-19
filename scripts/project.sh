#!/usr/bin/env bash
# agentvault overrides for ./dev. Sourced by scripts/cli.sh; see
# scripts/script-helpers/templates/dev-cli/project.sh.example for the contract.
#
# The Makefile targets are shims onto these verbs.

AV_APP=agentvault
AV_PKG=github.com/nikolareljin/agentvault/cmd

# The Go release go.mod asks for, e.g. "1.26.0".
av_go_required() {
  awk '$1 == "go" { print $2; exit }' "$DEV_REPO_ROOT/go.mod"
}

# Go 1.21+ downloads the toolchain go.mod names (GOTOOLCHAIN=auto), so any
# Go from 1.21 on can build this. Older or missing Go exits 1 naming the
# version to install.
av_require_go() {
  local want have major minor
  want="$(av_go_required)"
  if ! command -v go >/dev/null 2>&1; then
    log_error "Go is not installed. Install Go $want or newer: https://go.dev/dl/"
    exit 1
  fi
  have="$(go env GOVERSION 2>/dev/null)"
  have="${have#go}"
  major="${have%%.*}"
  minor="${have#*.}"
  minor="${minor%%.*}"
  if [[ ! "$major" =~ ^[0-9]+$ || ! "$minor" =~ ^[0-9]+$ ]] || (( major < 1 || (major == 1 && minor < 21) )); then
    log_error "Go ${have:-unknown} is too old to fetch the toolchain. Install Go $want or newer: https://go.dev/dl/"
    exit 1
  fi
}

av_ldflags() {
  local version commit date
  version="$(cat "$DEV_REPO_ROOT/VERSION")"
  commit="$(git -C "$DEV_REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo none)"
  date="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf -- '-s -w -X %s.Version=%s -X %s.Commit=%s -X %s.Date=%s' \
    "$AV_PKG" "$version" "$AV_PKG" "$commit" "$AV_PKG" "$date"
}

project_install() {
  log_info "install: submodules"
  git submodule update --init --recursive
  dev_install_hooks
  av_require_go
  log_info "install: go mod download"
  go mod download
  log_info "install: done. Build with ./dev build, install the binary with ./dev install-bin."
}

# ./dev build          pure-Go binary ./agentvault
# ./dev build bitnet   with the embedded llama.cpp engine ./agentvault-bitnet
project_build() {
  av_require_go
  if [[ "${DEV_ARGS[0]:-}" == "bitnet" ]]; then
    av_build_bitnet
    return
  fi
  go build -ldflags "$(av_ldflags)" -o "$AV_APP" .
  log_info "build: ./$AV_APP"
}

av_build_bitnet() {
  local llama="$DEV_REPO_ROOT/third_party/llama" f
  for f in lib/libllama.a lib/libggml.a lib/libggml-cpu.a include/llama.h; do
    if [[ ! -f "$llama/$f" ]]; then
      bash "$DEV_REPO_ROOT/scripts/build-llama.sh"
      break
    fi
  done
  # -lgomp is Linux-only; macOS uses Accelerate and libc++.
  local libs="-lllama -lggml -lggml-cpu -lm"
  if [[ "$(uname -s)" == "Darwin" ]]; then
    libs="$libs -lc++"
  else
    libs="$libs -lstdc++ -lgomp"
  fi
  CGO_ENABLED=1 CGO_CFLAGS="-I$llama/include" CGO_LDFLAGS="-L$llama/lib $libs" \
    go build -tags localllm -ldflags "$(av_ldflags)" -o "$AV_APP-bitnet" .
  log_info "build: ./$AV_APP-bitnet"
}

project_run() {
  av_require_go
  go run . "${DEV_ARGS[@]+"${DEV_ARGS[@]}"}"
}

project_test() {
  av_require_go
  go test ./...
}

# gofmt and go vet, and vet for the other release targets, so a
# darwin- or windows-only compile error fails here and not after a tag.
project_lint() {
  av_require_go
  local unformatted os
  unformatted="$(gofmt -l .)"
  if [[ -n "$unformatted" ]]; then
    log_error "lint: not gofmt-formatted:"
    printf '%s\n' "$unformatted" >&2
    return 1
  fi
  go vet ./...
  for os in darwin windows; do
    GOOS="$os" GOARCH=amd64 go vet ./...
  done
}

project_format() {
  gofmt -w .
}

project_preflight() {
  project_lint
  project_test
  log_info "preflight: govulncheck"
  go run golang.org/x/vuln/cmd/govulncheck@latest ./...
}

project_clean() {
  rm -f "$DEV_REPO_ROOT/$AV_APP" "$DEV_REPO_ROOT/$AV_APP-bitnet" "$DEV_REPO_ROOT/$AV_APP.exe"
  log_info "clean: removed built binaries. Vault and config are untouched."
}

# Copy the built binary to GOBIN, GOPATH/bin when it is on PATH, or
# ~/.local/bin.
project_install_bin() {
  project_build
  local target
  target="$(go env GOBIN)"
  if [[ -z "$target" ]]; then
    target="$(go env GOPATH)/bin"
    case ":$PATH:" in
      *":$target:"*) ;;
      *) target="$HOME/.local/bin" ;;
    esac
  fi
  mkdir -p "$target"
  cp "$AV_APP" "$target/$AV_APP"
  log_info "installed $AV_APP to $target/$AV_APP"
}

# Report what this machine has against what the repo needs.
project_doctor() {
  local rc=0 want
  want="$(av_go_required)"
  if command -v go >/dev/null 2>&1; then
    log_info "doctor: $(go env GOVERSION) (go.mod asks for $want; newer toolchains are fetched)"
  else
    log_error "doctor: Go missing; install Go $want or newer"
    rc=1
  fi
  if command -v git >/dev/null 2>&1; then
    log_info "doctor: $(git --version)"
  else
    log_error "doctor: git missing"
    rc=1
  fi
  if [[ -f "$SCRIPT_HELPERS_DIR/helpers.sh" ]]; then
    log_info "doctor: script-helpers at ${SCRIPT_HELPERS_DIR#"$DEV_REPO_ROOT"/}"
  fi
  command -v make >/dev/null 2>&1 || log_info "doctor: make not found (optional; ./dev covers every target)"
  return "$rc"
}

project_deploy() {
  not_applicable deploy "a CLI; releases ship from the release workflow"
}
