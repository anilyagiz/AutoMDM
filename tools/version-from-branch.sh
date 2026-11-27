#!/bin/sh
#
# version-from-branch.sh — derive a semver-compliant version string from a branch name.
#
# Usage:
#   version-from-branch.sh [BRANCH]
#
# If BRANCH is omitted the current git branch is used. Outputs a version, or blank if the version isn't supplied.
#
# This *will not* take tags into account, and will never deliver a "clean" (major.minor.patch) version.

set -e

BRANCH="${1:-$(git rev-parse --abbrev-ref HEAD)}"
TIMESTAMP="$(date -u +'%y%m%d%H%M')"

# 1. rc-major-automdm-vX.Y.Z, rc-minor-automdm-vX.Y.Z or rc-patch-automdm-vX.Y.Z → X.Y.Z-rc.YYMMDDhhmm
VERSION=$(echo "$BRANCH" | sed -E -n "s/^rc-(major|minor|patch)-automdm-v([0-9]+\.[0-9]+\.[0-9]+).*/\2-rc.${TIMESTAMP}/p")

if [ -z "$VERSION" ]; then
  # 2. X.Y.Z-anything or vX.Y.Z-anything → X.Y.Z+YYMMDDhhmm
  VERSION=$(echo "$BRANCH" | sed -E -n "s/^v?([0-9]+\.[0-9]+\.[0-9]+)[-+].*/\1+${TIMESTAMP}/p")
fi

echo "$VERSION"