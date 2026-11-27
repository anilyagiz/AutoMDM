#!/bin/bash

APP="/Applications/AutoMDM Desktop.app"
BIN="$APP/Contents/MacOS/AutoMDMDesktop"
PLIST="$APP/Contents/Info.plist"

if [ ! -x "$BIN" ]; then
  echo "AutoMDM Desktop is not installed."
  exit 100
fi

# Check if the installed AutoMDM Desktop supports notifications.
if ! plutil -extract AutoMDMDesktopCapabilities.notify raw -o - "$PLIST" >/dev/null 2>&1; then
  version=$(plutil -extract CFBundleShortVersionString raw -o - "$PLIST" 2>/dev/null)
  echo "AutoMDM Desktop ${version:-unknown} does not support notifications."
  exit 101
fi

# Get the user logged in at the GUI.
console_user=$(stat -f "%Su" /dev/console)
if [[ "$console_user" == "root" || "$console_user" == "loginwindow" || "$console_user" == "_mbsetupuser" ]]; then
  echo "No user is logged in at the GUI."
  exit 40
fi

console_uid=$(id -u "$console_user")

# Run the binary as the logged-in user.
launchctl asuser "$console_uid" sudo -u "$console_user" \
  "$BIN" notify --url "$AUTOMDM_VAR_PATCH_NOTIFICATION_URL"
