#!/usr/bin/env bash

#--------------------------------------------------------------
# This script helps with testing batch setting of configuration
# profiles via the AutoMDM API. Change this file as needed
# to generate different test cases.
#--------------------------------------------------------------

if [[ -z "$AUTOMDM_PATH" ]]; then
  echo "Error: AUTOMDM_PATH environment variable is not set. This is the path to the AutoMDM project." >&2
  exit 1
fi

if [[ -z "$AUTOMDM_SERVER_URL" ]]; then
  echo "Error: AUTOMDM_SERVER_URL environment variable is not set. This is the URL of the AutoMDM server." >&2
  exit 1
fi

if [[ -z "$AUTOMDM_AUTH_TOKEN" ]]; then
  echo "Error: AUTOMDM_AUTH_TOKEN environment variable is not set. This is the authentication token used for AutoMDM API requests." >&2
  exit 1
fi

# generate request payload
payload="$(
$AUTOMDM_PATH/tools/mdm/make_cfg_profiles.sh \
--file $AUTOMDM_PATH/it-and-security/lib/macos/configuration-profiles/1password-managed-settings.mobileconfig --name "1Password Managed Settings" \
--labels-type include_all --label "test label 2" --next \
--file $AUTOMDM_PATH/it-and-security/lib/windows/configuration-profiles/Enable\ Firewall.xml --name "Windows Enable Firewall" \
--labels-type include_any --label "test label 1" --next \
)"

# make request to AutoMDM API
curl -X POST "$AUTOMDM_SERVER_URL/api/latest/automdm/configuration_profiles/batch" \
-H "Content-Type: application/json" \
-H "Authorization: Bearer $AUTOMDM_AUTH_TOKEN" \
-d "$payload"
