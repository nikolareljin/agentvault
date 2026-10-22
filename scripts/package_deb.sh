#!/usr/bin/env bash
# Builds dist/agentvault_<VERSION>_<arch>.deb from the ./agentvault binary that
# `make build` produced. Run by the ci-helpers deb-build workflow as its
# build_command, in place of dpkg-buildpackage (this repo has no debian/).
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# "-" as "~", so a pre-release such as 0.15.0~rc1 sorts before 0.15.0.
version="$(tr -d '[:space:]' < "$repo/VERSION" | tr '-' '~')"
arch="$(dpkg --print-architecture)"
bin="$repo/agentvault"
# Inside the workspace: upload-artifact refuses a path with "..".
out="$repo/dist/agentvault_${version}_${arch}.deb"

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.+~][0-9A-Za-z.+~-]+)?$ ]] \
  || { echo "package_deb: VERSION '$version' is not a version" >&2; exit 1; }
[[ -x "$bin" ]] || { echo "package_deb: $bin missing; run make build first" >&2; exit 1; }

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
chmod 0755 "$stage"
install -Dm0755 "$bin" "$stage/usr/bin/agentvault"
install -Dm0644 "$repo/LICENSE" "$stage/usr/share/doc/agentvault/copyright"
mkdir -p "$stage/DEBIAN"
cat > "$stage/DEBIAN/control" <<CONTROL
Package: agentvault
Version: ${version}
Architecture: ${arch}
Maintainer: Nik Reljin <nikolareljin@users.noreply.github.com>
Section: utils
Priority: optional
Homepage: https://github.com/nikolareljin/agentvault
Description: CLI and TUI for managing agent configurations and instructions
 Command-line and terminal UI tool that keeps AI agent configuration and
 instruction files in one vault.
CONTROL
mkdir -p "$repo/dist"
dpkg-deb --root-owner-group --build "$stage" "$out"
echo "package_deb: $out"
