#!/usr/bin/env bash
# Guard against release tag collisions for release/X.Y.Z[-rcN] branches.
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: check_release_tag.sh [--branch <branch>] [--repo <path>] [--fetch-tags] [--print-version]

Options:
  --branch <branch>   Release branch name. Defaults to GITHUB_HEAD_REF/GITHUB_REF_NAME.
  --repo <path>       Repository path. Defaults to GITHUB_WORKSPACE or current directory.
  --fetch-tags        Fetch tags before checking.
  --print-version     Print parsed release version when check passes.
  -h, --help          Show help.
USAGE
}

log_info() {
  echo "[INFO] $*" >&2
}

log_error() {
  echo "[ERROR] $*" >&2
}

branch=""
repo_dir="${GITHUB_WORKSPACE:-$(pwd)}"
fetch_tags=false
print_version=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --branch)
      if [[ $# -lt 2 || -z "${2:-}" ]]; then
        log_error "Missing value for --branch"
        usage
        exit 2
      fi
      branch="$2"
      shift 2
      ;;
    --repo)
      if [[ $# -lt 2 || -z "${2:-}" ]]; then
        log_error "Missing value for --repo"
        usage
        exit 2
      fi
      repo_dir="$2"
      shift 2
      ;;
    --fetch-tags)
      fetch_tags=true
      shift
      ;;
    --print-version)
      print_version=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      log_error "Unknown argument: $1"
      usage
      exit 2
      ;;
  esac
done

if [[ -z "$branch" ]]; then
  branch="${GITHUB_HEAD_REF:-${GITHUB_REF_NAME:-}}"
fi

if [[ -z "$branch" ]]; then
  log_error "Branch not provided and GITHUB_REF_NAME/GITHUB_HEAD_REF not set"
  exit 2
fi

if [[ ! "$branch" =~ ^release/v?([0-9]+\.[0-9]+\.[0-9]+(-rc\.?[0-9]+)?)$ ]]; then
  log_info "Skipping: '$branch' is not a release branch"
  exit 0
fi

version="${BASH_REMATCH[1]}"

if ! git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  log_error "Repository path '$repo_dir' is not a valid git worktree"
  exit 1
fi

if $fetch_tags; then
  if ! git -C "$repo_dir" fetch --tags --prune --force >/dev/null 2>&1; then
    log_error "Failed to fetch tags from repository at '$repo_dir'"
    exit 1
  fi
fi

if git -C "$repo_dir" show-ref --tags -q "refs/tags/$version"; then
  log_error "Tag $version already exists for release branch $branch"
  exit 1
fi

log_info "Tag $version is available for release branch $branch"
if $print_version; then
  echo "$version"
fi
