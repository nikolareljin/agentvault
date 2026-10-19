#!/usr/bin/env bash
# SCRIPT: install_check.sh
# DESCRIPTION: Build and install agentvault into a scratch GOBIN on this
#              machine, then run the installed binary.
# USAGE: tests/machine/install_check.sh
# EXIT_CODES:
#   0  installed binary runs and reports the VERSION file's version
#   1  install or the installed binary failed
#   2  cannot run here (no Go)
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
command -v go >/dev/null 2>&1 || { echo "install_check: no Go on PATH" >&2; exit 2; }

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

if ! GOBIN="$scratch/bin" "$root/dev" install-bin; then
  echo "install_check: ./dev install-bin failed" >&2
  exit 1
fi
bin="$scratch/bin/agentvault"
[[ -x "$bin" ]] || { echo "install_check: $bin missing after install" >&2; exit 1; }

want="$(cat "$root/VERSION")"
if ! out="$("$bin" version 2>&1)"; then
  echo "install_check: installed binary failed: $out" >&2
  exit 1
fi
case "$out" in
  *"$want"*) echo "install_check: ok ($(uname -s) $(uname -m)): $out" ;;
  *) echo "install_check: version output '$out' does not name $want" >&2; exit 1 ;;
esac
