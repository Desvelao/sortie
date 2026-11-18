#!/usr/bin/env bash
# Version parity check: one VERSION (x.y.z) for both apps, and both changelogs kept up to date.
# Usage: scripts/check-version.sh [tag]   (tag like v1.2.3: must equal VERSION)
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { echo "version check failed: $*" >&2; exit 1; }

version=$(tr -d '[:space:]' < VERSION)
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "VERSION must look like x.y.z, got '$version'"

if [[ -n "${1:-}" ]]; then
  [[ "${1#v}" == "$version" ]] || fail "tag $1 does not match VERSION ($version)"
fi

for app in desktop android; do
  log="$app/CHANGELOG.md"
  [[ -f "$log" ]] || fail "$log is missing"
  grep -q '^## \[Unreleased\]' "$log" || fail "$log has no '## [Unreleased]' section"
  # A tagged release needs its own section in both changelogs (keeps the apps in step).
  if [[ -n "${1:-}" ]]; then
    grep -qE "^## \[${version//./\\.}\]" "$log" || fail "$log has no '## [$version]' section"
  fi
done

echo "version $version OK"
