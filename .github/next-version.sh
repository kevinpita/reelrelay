#!/usr/bin/env bash
# Print the next release version.
# Usage: next-version.sh <minimum-version> < tag-list
# The result is the minimum version, or the highest vX.Y.Z tag plus one patch
# when that tag is not lower than the minimum.
set -euo pipefail

semver='^[0-9]+\.[0-9]+\.[0-9]+$'
minimum="$1"
[[ "$minimum" =~ $semver ]] || {
  echo "minimum version must be X.Y.Z, got '$minimum'" >&2
  exit 1
}

latest="$(sed -n 's/^v//p' | grep -E "$semver" | sort -V | tail -n 1 || true)"
if [[ -z "$latest" || "$(printf '%s\n' "$latest" "$minimum" | sort -V | tail -n 1)" != "$latest" ]]; then
  echo "$minimum"
else
  echo "${latest%.*}.$((${latest##*.} + 1))"
fi
