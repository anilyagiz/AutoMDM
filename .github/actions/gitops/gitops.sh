#!/usr/bin/env bash

# -e: Immediately exit if any command has a non-zero exit status.
# -x: Print all executed commands to the terminal.
# -u: Exit if an undefined variable is used.
# -o pipefail: Exit if any command in a pipeline fails.
set -exuo pipefail

AUTOMDM_GITOPS_DIR="${AUTOMDM_GITOPS_DIR:-.}"
AUTOMDM_GLOBAL_FILE="${AUTOMDM_GLOBAL_FILE:-$AUTOMDM_GITOPS_DIR/default.yml}"
AUTOMDMCTL="${AUTOMDMCTL:-automdmctl}"
AUTOMDM_DRY_RUN_ONLY="${AUTOMDM_DRY_RUN_ONLY:-false}"
AUTOMDM_DELETE_OTHER_TEAMS="${AUTOMDM_DELETE_OTHER_TEAMS:-true}"

# Check for existence of the global file in case the script is used
# on repositories with team only yamls.
if [ -f "$AUTOMDM_GLOBAL_FILE" ]; then
	# Validate that global file contains org_settings
	grep -Exq "^org_settings:.*" "$AUTOMDM_GLOBAL_FILE"
else
	AUTOMDM_DELETE_OTHER_TEAMS=false
fi

# If you are using secrets to manage SSO metadata for AutoMDM SSO login or MDM SSO login, uncomment the below:

# AUTOMDM_SSO_METADATA=$( sed '2,$s/^/      /' <<<  "${AUTOMDM_MDM_SSO_METADATA}")
# AUTOMDM_MDM_SSO_METADATA=$( sed '2,$s/^/        /' <<<  "${AUTOMDM_MDM_SSO_METADATA}")

# Copy/pasting raw SSO metadata into GitHub secrets will result in malformed yaml.
# Adds spaces to all but the first line of metadata keeps the  multiline string in bounds.

if compgen -G "$AUTOMDM_GITOPS_DIR"/teams/*.yml > /dev/null; then
  # Validate that every team has a unique name.
  # This is a limited check that assumes all automdm files contain the phrase: `name: <team_name>`
  if [ -n "$(perl -nle 'print $1 if /^name:\s*(.+)$/' "$AUTOMDM_GITOPS_DIR"/teams/*.yml | sort | uniq -d)" ]; then
    echo "Error: duplicate automdm names found" >&2
    exit 1
  fi
fi

args=()
if [ -f "$AUTOMDM_GLOBAL_FILE" ]; then
	args=(-f "$AUTOMDM_GLOBAL_FILE")
fi

for automdm_file in "$AUTOMDM_GITOPS_DIR"/teams/*.yml; do
  if [ -f "$automdm_file" ]; then
    args+=(-f "$automdm_file")
  fi
done
if [ "$AUTOMDM_DELETE_OTHER_TEAMS" = true ]; then
  args+=(--delete-other-teams)
fi

# Dry run
$AUTOMDMCTL gitops "${args[@]}" --dry-run
if [ "$AUTOMDM_DRY_RUN_ONLY" = true ]; then
  exit 0
fi

# Real run
$AUTOMDMCTL gitops "${args[@]}"
