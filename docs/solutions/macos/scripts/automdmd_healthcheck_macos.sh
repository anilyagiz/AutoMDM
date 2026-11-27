#!/usr/bin/env bash
# automdmd_healthcheck_macos.sh
#
# Checks the health of all automdmd components on macOS and collects logs into
# a timestamped archive for support/troubleshooting. macOS counterpart to
# linux_ubuntu_automdmd_healthcheck.sh / automdmd-healthcheck.ps1.
#
# Components checked:
#   - com.fleetdm.orbit   (LaunchDaemon, runs orbit as root)
#   - AutoMDM (orbit)       (process: /opt/orbit/bin/orbit/AutoMDM or /opt/orbit/bin/orbit/orbit)
#   - osqueryd            (process: spawned and managed by orbit, runs from an
#                          app bundle under /opt/orbit/bin/osqueryd/macos-app/)
#   - automdm-desktop       (process: optional, only present if packaged with
#                          --automdm-desktop; started by orbit, not its own LaunchAgent)
#   - automdmd installation (pkg receipt, /var/log/install.log, MDM delivery of
#                          automdmd via InstallEnterpriseApplication)
#
# Sources:
#   - LaunchDaemon:        /Library/LaunchDaemons/com.fleetdm.orbit.plist
#                          (Label com.fleetdm.orbit, ProgramArguments
#                          /opt/orbit/bin/orbit/AutoMDM or /opt/orbit/bin/orbit/orbit)
#   - Install root:        /opt/orbit (orbit/CHANGELOG.md — installer paths)
#   - Orbit symlink:       /usr/local/bin/orbit
#   - Pkg receipt:         <identifier>.base.pkg, com.fleetdm.orbit.base.pkg by default
#   - Enroll secret:       /opt/orbit/secret.txt (ORBIT_ENROLL_SECRET_PATH).
#                          On macOS this is removed after first successful
#                          enrollment and the secret is kept in the login
#                          keychain instead — so an absent file is expected,
#                          not a failure. https://automdm.com/guides/automdmd-authentication
#   - Orbit node key:      /opt/orbit/secret-orbit-node-key.txt
#   - Log paths:           https://automdm.com/guides/automdm-troubleshooting-for-it-admins
#                            - orbit stdout/stderr: /var/log/orbit/orbit.stdout.log,
#                              /var/log/orbit/orbit.stderr.log (set in the LaunchDaemon plist)
#                            - osquery filesystem logger: /opt/orbit/osquery_log
#                            - automdm-desktop: $HOME/Library/Logs/AutoMDM/automdm-desktop.log (per user)
#   - Profile dumps:       /opt/orbit/profiles (pkill -USR1 AutoMDM, or pkill -USR1 orbit)
#
# Also collects a lookback window (default 72h, override with LOOKBACK_HOURS env
# var) of system events — reboots, installer activity, launchd job failures,
# unified log errors — to help correlate a reported problem with what changed
# beforehand. automdmd install history uses a longer window (default 14 days,
# INSTALL_LOOKBACK_DAYS) because hosts often fail at enrollment, days before
# anyone runs this script.
#
# Must be run as root (sudo).

# No `set -e`: a diagnostic script should keep going and report as much as it
# can, rather than abort on the first unexpected non-zero exit.
set -uo pipefail

# Never call `log` unqualified in this script: a shell function or alias named
# `log` would shadow macOS's /usr/bin/log and silently produce empty log files.
LOG_BIN="/usr/bin/log"

# ── Output helpers ─────────────────────────────────────────────────────────────
# Colour only when writing to a terminal, so pasted/redirected output stays clean.
if [[ -t 1 ]]; then
  RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
else
  RED=''; GREEN=''; YELLOW=''; NC=''
fi
# Every line shown to the user also goes to summary.txt (without colour codes).
# printf, not `echo -e`: run as `sh script.sh`, macOS sh prints "-e" literally.
status() { printf "  ${2}%-6s${NC}  %s\n" "$1" "$3"; printf "  %-6s  %s\n" "$1" "$3" >> "${SUMMARY}"; }
out()  { printf '%s\n' "$*"; printf '%s\n' "$*" >> "${SUMMARY}"; }
ok()   { status "[OK]"   "${GREEN}"  "$*"; }
warn() { status "[WARN]" "${YELLOW}" "$*"; }
fail() { status "[FAIL]" "${RED}"    "$*"; }
info() { status "[INFO]" ""          "$*"; }
# Indented multi-line output (every line indented, not just the first).
# Detail lines (PIDs, paths, command output) go to summary.txt only, so the
# console shows just the status lines.
indent() { sed 's/^/    /' >> "${SUMMARY}"; }
# orbit reads booleans with Go's strconv.ParseBool: 1, t, true in any case.
# (tr, not ${1,,}: macOS ships bash 3.2.)
is_true() { case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in 1|t|true) return 0 ;; esac; return 1; }

if [[ $EUID -ne 0 ]]; then
  echo "This script must be run as root (sudo)." >&2
  exit 1
fi

LOOKBACK_HOURS="${LOOKBACK_HOURS:-72}"
INSTALL_LOOKBACK_DAYS="${INSTALL_LOOKBACK_DAYS:-14}"

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
HOSTNAME_SAFE=$(hostname | tr '.' '_')
ARCHIVE_NAME="automdmd_healthcheck_${HOSTNAME_SAFE}_${TIMESTAMP}"
# Without a work dir every path below would resolve under /, and this runs
# as root, so stop here rather than collect best-effort.
WORK_DIR=$(mktemp -d "/tmp/${ARCHIVE_NAME}.XXXXXX") && [[ -n "${WORK_DIR}" ]] || {
  echo "Cannot create a work directory under /tmp (full or read-only?). Exiting." >&2
  exit 1
}
SUMMARY="${WORK_DIR}/summary.txt"
OVERALL_EXIT=0

ORBIT_ROOT="/opt/orbit"
LAUNCH_DAEMON_LABEL="com.fleetdm.orbit"
LAUNCH_DAEMON_PLIST="/Library/LaunchDaemons/${LAUNCH_DAEMON_LABEL}.plist"
# Take the binary path from the LaunchDaemon when present, so a future rename
# of the orbit binary doesn't break the checks below.
ORBIT_BIN=$(/usr/libexec/PlistBuddy -c "Print :ProgramArguments:0" "${LAUNCH_DAEMON_PLIST}" 2>/dev/null || true)
ORBIT_BIN="${ORBIT_BIN:-${ORBIT_ROOT}/bin/orbit/orbit}"

# ── Header ─────────────────────────────────────────────────────────────────────
out "============================================================"
out " AutoMDM automdmd Health Check"
out " Host:      $(hostname)"
out " Date:      $(date)"
out " macOS:     $(sw_vers -productName) $(sw_vers -productVersion) ($(sw_vers -buildVersion))"
out " Kernel:    $(uname -r)"
out " Serial:    $(ioreg -c IOPlatformExpertDevice -d 2 | awk -F'"' '/IOPlatformSerialNumber/ {print $4}')"
out "============================================================"
out ""

# ══════════════════════════════════════════════════════════════════════════════
# 1. LAUNCHDAEMON
# ══════════════════════════════════════════════════════════════════════════════
out "── 1. LaunchDaemon (${LAUNCH_DAEMON_LABEL}) ─────────────────"

if [[ -f "${LAUNCH_DAEMON_PLIST}" ]]; then
  ok "LaunchDaemon plist found: ${LAUNCH_DAEMON_PLIST}"
else
  fail "LaunchDaemon plist not found: ${LAUNCH_DAEMON_PLIST}"
  OVERALL_EXIT=1
fi

LAUNCHCTL_PRINT=$(launchctl print "system/${LAUNCH_DAEMON_LABEL}" 2>&1 || true)
if echo "${LAUNCHCTL_PRINT}" | grep -q "state = running"; then
  ok "${LAUNCH_DAEMON_LABEL} is running"
elif [[ -n "${LAUNCHCTL_PRINT}" ]] && ! echo "${LAUNCHCTL_PRINT}" | grep -qi "could not find"; then
  fail "${LAUNCH_DAEMON_LABEL} is loaded but not running"
  # last exit code / reason explain crash loops
  echo "${LAUNCHCTL_PRINT}" | grep -E "last exit|state =|runs =" | indent
  OVERALL_EXIT=1
else
  fail "${LAUNCH_DAEMON_LABEL} is NOT loaded in launchd"
  OVERALL_EXIT=1
fi
echo "${LAUNCHCTL_PRINT}" > "${WORK_DIR}/launchctl_print.txt"

if [[ -f "${LAUNCH_DAEMON_PLIST}" ]]; then
  RUN_AT_LOAD=$(/usr/libexec/PlistBuddy -c "Print :RunAtLoad" "${LAUNCH_DAEMON_PLIST}" 2>/dev/null || echo "unknown")
  KEEP_ALIVE=$(/usr/libexec/PlistBuddy -c "Print :KeepAlive" "${LAUNCH_DAEMON_PLIST}" 2>/dev/null || echo "unknown")
  if [[ "${RUN_AT_LOAD}" == "true" ]]; then
    ok "RunAtLoad is true — will start on boot"
  else
    warn "RunAtLoad is '${RUN_AT_LOAD}' — may not start on boot"
  fi
  info "KeepAlive: ${KEEP_ALIVE}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# 2. PROCESS CHECKS
# ══════════════════════════════════════════════════════════════════════════════
out ""
out "── 2. Processes ────────────────────────────────────────────"

# PID and executable only. On macOS, `pgrep -l`/`ps -o args` can also show a
# process's environment, and osqueryd's includes ENROLL_SECRET.
# macOS `ps -o comm=` prints the full executable path.
show_pids() {
  local pid
  for pid in $1; do ps -o pid=,comm= -p "${pid}" 2>/dev/null; done | sed 's/^ *//' | indent
}

# PIDs from pgrep whose executable (not just command line) starts with $2. An
# editor with a file under /opt/orbit open would otherwise count as running.
pids_with_exe() {
  local pid exe out=""
  for pid in $1; do
    exe=$(ps -o comm= -p "${pid}" 2>/dev/null) || continue
    case "${exe}" in "$2"*) out="${out} ${pid}" ;; esac
  done
  echo "${out# }"
}

check_process() {
  local label="$1"
  local prefix="$2"
  local pids
  # -a: macOS pgrep skips its own ancestors by default, and when AutoMDM runs this
  # script, orbit is an ancestor.
  pids=$(pids_with_exe "$(pgrep -a -f "${prefix}" 2>/dev/null || true)" "${prefix}")
  if [[ -n "${pids}" ]]; then
    ok "${label} is running"
    show_pids "${pids}"
  else
    fail "${label} is NOT running (no process running from ${prefix})"
    OVERALL_EXIT=1
  fi
}

# After an auto-update, orbit runs from bin/orbit/<platform>/<channel>/orbit,
# not the bin/orbit/orbit path in the LaunchDaemon, so match the folder.
check_process "orbit"    "$(dirname "${ORBIT_BIN}")/"

# Match orbit's own osqueryd only: another osquery install (standalone osquery,
# another vendor's agent) must not count as automdmd being healthy.
check_process "osqueryd (automdmd)" "${ORBIT_ROOT}/bin/osqueryd/"

# Decide by executable path, not command line: osqueryd's command line output
# on macOS can include its environment, ENROLL_SECRET among it.
OTHER_OSQUERYD=""
for pid in $(pgrep -x osqueryd 2>/dev/null || true); do
  # Skip a process that exited before the lookup.
  exe=$(ps -o comm= -p "${pid}" 2>/dev/null) || continue
  case "${exe}" in
    ""|"${ORBIT_ROOT}/bin/osqueryd/"*) ;;
    *) OTHER_OSQUERYD="${OTHER_OSQUERYD} ${pid}" ;;
  esac
done
if [[ -n "${OTHER_OSQUERYD// /}" ]]; then
  warn "osqueryd running from outside ${ORBIT_ROOT} (another osquery install?), PIDs in summary.txt"
  show_pids "${OTHER_OSQUERYD}"
fi

# automdm-desktop: only present if package was built with --automdm-desktop;
# started by orbit itself rather than its own LaunchAgent
DESKTOP_PIDS=$(pids_with_exe "$(pgrep -f "automdm-desktop" 2>/dev/null || true)" "${ORBIT_ROOT}/bin/desktop/")
if [[ -n "${DESKTOP_PIDS}" ]]; then
  ok "automdm-desktop is running"
  show_pids "${DESKTOP_PIDS}"
else
  warn "automdm-desktop is NOT running (expected if not packaged with --automdm-desktop)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# 3. KEY FILES
# ══════════════════════════════════════════════════════════════════════════════
out ""
out "── 3. Key files and directories ────────────────────────────"

check_file() {
  local label="$1"
  local path="$2"
  if [[ -e "${path}" ]]; then
    ok "${label}: ${path}"
  else
    fail "${label} not found: ${path}"
    OVERALL_EXIT=1
  fi
}

check_file "orbit binary"     "${ORBIT_BIN}"
check_file "orbit symlink"    "/usr/local/bin/orbit"
check_file "osquery pidfile"  "${ORBIT_ROOT}/osquery.pid"

# Enroll secret is intentionally removed from disk after first enrollment on
# macOS (moved to the login keychain), so its absence is normal.
if [[ -f "${ORBIT_ROOT}/secret.txt" ]]; then
  info "enroll secret still on disk: ${ORBIT_ROOT}/secret.txt (expected before first enrollment)"
else
  info "enroll secret not on disk (expected after enrollment — stored in keychain)"
fi

# Report orbit node key presence without printing value
if [[ -s "${ORBIT_ROOT}/secret-orbit-node-key.txt" ]]; then
  ok "orbit node key is non-empty (enrolled)"
else
  fail "orbit node key is missing or empty (not enrolled): ${ORBIT_ROOT}/secret-orbit-node-key.txt"
  OVERALL_EXIT=1
fi

if [[ -d "${ORBIT_ROOT}" ]]; then
  ls -laR "${ORBIT_ROOT}" 2>/dev/null | grep -v -i "secret" > "${WORK_DIR}/opt_orbit_listing.txt" || true
fi

# ══════════════════════════════════════════════════════════════════════════════
# 4. LAUNCHDAEMON CONFIGURATION SUMMARY (REDACTED)
# ══════════════════════════════════════════════════════════════════════════════
out ""
out "── 4. LaunchDaemon configuration (redacted) ────────────────"
AUTOMDM_URL=""; AUTOMDM_CERT=""; AUTOMDM_INSECURE=""
if [[ -f "${LAUNCH_DAEMON_PLIST}" ]]; then
  # In XML plists the value is on the line after its <key>, so redact the
  # <string> that follows any secret-looking key (except *_PATH, which is a path).
  # Convert first, so an unreadable plist is reported instead of "Collected".
  if PLIST_XML=$(plutil -convert xml1 -o - "${LAUNCH_DAEMON_PLIST}" 2>/dev/null); then
    printf '%s\n' "${PLIST_XML}" \
      | awk '
          redact && /<string>/ { sub(/<string>.*<\/string>/, "<string>REDACTED</string>"); redact=0 }
          /<key>/ { k=toupper($0); redact = (k ~ /SECRET|PASSWORD|TOKEN|KEY/ && k !~ /_PATH</) }
          { print }' \
      | indent >/dev/null
    ok "Collected redacted LaunchDaemon plist contents (in summary.txt)"
  else
    warn "Could not read ${LAUNCH_DAEMON_PLIST} with plutil"
  fi
  AUTOMDM_URL=$(/usr/libexec/PlistBuddy -c "Print :EnvironmentVariables:ORBIT_AUTOMDM_URL" "${LAUNCH_DAEMON_PLIST}" 2>/dev/null || true)
  AUTOMDM_CERT=$(/usr/libexec/PlistBuddy -c "Print :EnvironmentVariables:ORBIT_AUTOMDM_CERTIFICATE" "${LAUNCH_DAEMON_PLIST}" 2>/dev/null || true)
  AUTOMDM_INSECURE=$(/usr/libexec/PlistBuddy -c "Print :EnvironmentVariables:ORBIT_INSECURE" "${LAUNCH_DAEMON_PLIST}" 2>/dev/null || true)
else
  fail "${LAUNCH_DAEMON_PLIST} not found"
  OVERALL_EXIT=1
fi

# ══════════════════════════════════════════════════════════════════════════════
# 5. ORBIT VERSION
# ══════════════════════════════════════════════════════════════════════════════
out ""
out "── 5. Orbit version ────────────────────────────────────────"
# Call the real binary rather than the PATH symlink, so a missing symlink
# doesn't hide the version.
if [[ -x "${ORBIT_BIN}" ]]; then
  ORBIT_VERSION=$("${ORBIT_BIN}" version 2>/dev/null || echo "unknown")
  info "$(echo "${ORBIT_VERSION}" | head -1)"
  echo "${ORBIT_VERSION}" | indent
else
  warn "orbit binary not found at ${ORBIT_BIN}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# 6. AUTOMDMD INSTALLATION, MDM, AND CONNECTIVITY
# ══════════════════════════════════════════════════════════════════════════════
# With automatic (ADE) enrollment, AutoMDM installs automdmd with an MDM
# InstallEnterpriseApplication command. A host can then be MDM-enrolled with no
# automdmd on disk. These checks tell "never installed" from "installed but
# not running".
out ""
out "── 6. automdmd installation, MDM, and connectivity ──────────"
INSTALL_DIR="${WORK_DIR}/logs/install"
mkdir -p "${INSTALL_DIR}"

AUTOMDMD_PKGS=$(pkgutil --pkgs 2>/dev/null | grep -i -E 'automdm|orbit' || true)
# A package built with a custom identifier has a receipt without automdm/orbit in
# its name, so also look the receipt up by a file the package installs.
if [[ -z "${AUTOMDMD_PKGS}" && -f "${LAUNCH_DAEMON_PLIST}" ]]; then
  AUTOMDMD_PKGS=$(pkgutil --file-info "${LAUNCH_DAEMON_PLIST}" 2>/dev/null | sed -n 's/^pkgid: //p' || true)
fi
if [[ -n "${AUTOMDMD_PKGS}" ]]; then
  ok "automdmd package receipt(s) found: $(printf '%s\n' "${AUTOMDMD_PKGS}" | paste -sd, - | sed 's/,/, /g')"
  while read -r pkg; do
    pkgutil --pkg-info "${pkg}" >> "${INSTALL_DIR}/pkg_receipts.txt" 2>&1 || true
  done <<< "${AUTOMDMD_PKGS}"
elif [[ -e "${ORBIT_BIN}" || -f "${LAUNCH_DAEMON_PLIST}" ]]; then
  warn "No automdmd package receipt found, but automdmd files are present (installed without a receipt, or receipt removed)"
else
  fail "No automdmd package receipt and no automdmd files: the automdmd package was never installed on this Mac"
  OVERALL_EXIT=1
fi

# /var/log/install.log is searched in full (including rotated copies), not
# just the lookback window: the failed install is usually from enrollment day.
: > "${INSTALL_DIR}/install_log_automdm.log"
for f in /var/log/install.log /var/log/install.log.*; do
  [[ -f "${f}" ]] || continue
  case "${f}" in
    *.gz|*.bz2) zgrep -i -E 'automdm|orbit' "${f}" >> "${INSTALL_DIR}/install_log_automdm.log" 2>/dev/null || true ;;
    *)          grep  -i -E 'automdm|orbit' "${f}" >> "${INSTALL_DIR}/install_log_automdm.log" 2>/dev/null || true ;;
  esac
done
INSTALL_LOG_LINES=$(wc -l < "${INSTALL_DIR}/install_log_automdm.log" | tr -d ' ')
if [[ "${INSTALL_LOG_LINES}" -gt 0 ]]; then
  info "${INSTALL_LOG_LINES} line(s) mentioning automdm/orbit in /var/log/install.log* (last 20 in summary.txt, all in logs/install/install_log_automdm.log)"
  tail -20 "${INSTALL_DIR}/install_log_automdm.log" | indent
else
  warn "No mention of automdm/orbit in /var/log/install.log*: the installer never ran for automdmd (or the log has rotated away)"
fi

# MDM enrollment state (works without automdmd)
MDM_STATUS=$(profiles status -type enrollment 2>&1 || true)
echo "${MDM_STATUS}" > "${INSTALL_DIR}/mdm_enrollment_status.txt"
if echo "${MDM_STATUS}" | grep -q "MDM enrollment: Yes"; then
  ok "Mac is MDM enrolled"
else
  warn "Mac is not MDM enrolled"
fi
echo "${MDM_STATUS}" | indent

# MDM delivery of automdmd: mdmclient receives InstallEnterpriseApplication, then
# the pkg is downloaded and handed to installd.
"${LOG_BIN}" show --last "${INSTALL_LOOKBACK_DAYS}d" --style syslog --predicate \
  '(process == "mdmclient" OR process == "storedownloadd" OR process == "appstored" OR process == "installd") AND (eventMessage CONTAINS[c] "automdm" OR eventMessage CONTAINS[c] "orbit" OR eventMessage CONTAINS[c] "InstallEnterpriseApplication")' \
  > "${INSTALL_DIR}/mdm_automdmd_delivery.log" 2>&1 || true
DELIVERY_LINES=$(grep -c -i -E 'automdm|orbit|InstallEnterpriseApplication' "${INSTALL_DIR}/mdm_automdmd_delivery.log" || true)
info "${DELIVERY_LINES:-0} unified log line(s) about MDM delivery/install of automdmd in last ${INSTALL_LOOKBACK_DAYS}d (logs/install/mdm_automdmd_delivery.log)"

# AutoMDM server reachability. Without the LaunchDaemon plist, fall back to the
# MDM server's origin, which is the same AutoMDM server.
if [[ -z "${AUTOMDM_URL}" ]]; then
  AUTOMDM_URL=$(echo "${MDM_STATUS}" | sed -n 's|^MDM server: \(https://[^/]*\).*|\1|p' | head -1)
fi
if [[ -n "${AUTOMDM_URL}" ]]; then
  # Probe the server the way orbit connects: https:// when no scheme is given,
  # the package's own CA file when set, and no verification when insecure.
  [[ "${AUTOMDM_URL}" == http* ]] || AUTOMDM_URL="https://${AUTOMDM_URL}"
  CURL_TLS=()
  [[ -n "${AUTOMDM_CERT}" && -f "${AUTOMDM_CERT}" ]] && CURL_TLS+=(--cacert "${AUTOMDM_CERT}")
  is_true "${AUTOMDM_INSECURE}" && CURL_TLS+=(-k)
  HTTP_CODE=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 "${CURL_TLS[@]+"${CURL_TLS[@]}"}" "${AUTOMDM_URL%/}/healthz" 2>"${INSTALL_DIR}/curl_error.txt" || true)
  if [[ "${HTTP_CODE}" =~ ^[1-5][0-9][0-9]$ && "${HTTP_CODE}" != "000" ]]; then
    ok "AutoMDM server reachable: ${AUTOMDM_URL} (HTTP ${HTTP_CODE} from /healthz)"
  else
    fail "Cannot reach AutoMDM server ${AUTOMDM_URL}: $(head -1 "${INSTALL_DIR}/curl_error.txt")"
    OVERALL_EXIT=1
  fi
else
  warn "AutoMDM server URL unknown (no LaunchDaemon plist and no MDM server) — skipped reachability check"
fi

# ══════════════════════════════════════════════════════════════════════════════
# 7. LOG COLLECTION
# ══════════════════════════════════════════════════════════════════════════════
out ""
out "── 7. Log collection ───────────────────────────────────────"

collect_log() {
  local label="$1"
  local src="$2"
  local dest_dir="$3"
  # A log can rotate between the check and the copy, so report copy failures.
  if [[ -f "${src}" ]]; then
    mkdir -p "${dest_dir}"
    if cp "${src}" "${dest_dir}/" 2>/dev/null; then
      ok "Collected ${label}: ${src}"
    else
      warn "Failed to copy ${label}: ${src}"
    fi
  elif [[ -d "${src}" ]]; then
    mkdir -p "${dest_dir}"
    if cp -r "${src}/." "${dest_dir}/" 2>/dev/null; then
      ok "Collected ${label} directory: ${src}"
    else
      warn "Collected ${label} directory only partly (some files failed to copy): ${src}"
    fi
  else
    warn "${label} not found at ${src} (skipping)"
  fi
}

# orbit stdout/stderr — paths set via StandardOutPath/StandardErrorPath in the
# LaunchDaemon plist
collect_log "orbit stdout log" "/var/log/orbit/orbit.stdout.log" "${WORK_DIR}/logs/orbit"
collect_log "orbit stderr log" "/var/log/orbit/orbit.stderr.log" "${WORK_DIR}/logs/orbit"

# osquery filesystem logger output — only populated when logger_path/logger_plugin
# is set to "filesystem" in agent options.
collect_log "osquery filesystem logger" "${ORBIT_ROOT}/osquery_log" "${WORK_DIR}/logs/osquery_log"

# automdm-desktop runs per-user; sweep all user home directories for its log file
DESKTOP_LOGS_FOUND=0
for user_home in /Users/*; do
  [[ -d "${user_home}" ]] || continue
  desktop_log="${user_home}/Library/Logs/AutoMDM/automdm-desktop.log"
  if [[ -f "${desktop_log}" ]]; then
    user_name=$(basename "${user_home}")
    dest_dir="${WORK_DIR}/logs/automdm-desktop/${user_name}"
    mkdir -p "${dest_dir}"
    if cp "${desktop_log}" "${dest_dir}/" 2>/dev/null; then
      ok "Collected automdm-desktop log for user ${user_name}: ${desktop_log}"
      DESKTOP_LOGS_FOUND=1
    else
      warn "Failed to copy automdm-desktop log for user ${user_name}: ${desktop_log}"
    fi
  fi
done
if [[ "${DESKTOP_LOGS_FOUND}" -eq 0 ]]; then
  warn "No automdm-desktop.log found under any user's Library/Logs/AutoMDM (expected if automdm-desktop is not installed)"
fi

# Unified log entries from automdmd's own processes (orbit, osqueryd,
# automdm-desktop all run from /opt/orbit). Matching message text instead
# ("team", "osquery") pulls in most of the system log: about 800,000 lines and
# several minutes for 72h on a lab Mac, past AutoMDM's script timeout.
mkdir -p "${WORK_DIR}/logs/unified_log"
UNIFIED_LOG="${WORK_DIR}/logs/unified_log/automdmd.log"
if "${LOG_BIN}" show --predicate "processImagePath BEGINSWITH \"${ORBIT_ROOT}/\" OR senderImagePath BEGINSWITH \"${ORBIT_ROOT}/\"" \
    --style syslog --last "${LOOKBACK_HOURS}h" > "${UNIFIED_LOG}" 2>&1; then
  ok "Collected unified log entries from automdmd processes (last ${LOOKBACK_HOURS}h, $(wc -l < "${UNIFIED_LOG}" | tr -d ' ') lines)"
else
  warn "Failed to query unified log (log show) — see unified_log/automdmd.log for details"
fi

# ══════════════════════════════════════════════════════════════════════════════
# 8. SYSTEM EVENTS (lookback window)
# ══════════════════════════════════════════════════════════════════════════════
# Captures what changed on the box before the user noticed a problem — reboots,
# installer activity, launchd job failures, and unified log errors. Users
# rarely remember every change; having this saves a round trip of clarifying
# questions when triaging a report.
out ""
out "── 8. System events, last ${LOOKBACK_HOURS}h ───────────────"

SYSEV_DIR="${WORK_DIR}/logs/system_events"
mkdir -p "${SYSEV_DIR}"

# Reboots/shutdowns (macOS `last` has no -x; reboot/shutdown are pseudo-users)
if command -v last >/dev/null 2>&1; then
  last reboot shutdown 2>/dev/null | head -20 > "${SYSEV_DIR}/reboots.log" || true
  if [[ -s "${SYSEV_DIR}/reboots.log" ]]; then
    ok "Collected reboot/shutdown history"
  else
    warn "No reboot/shutdown history from \`last\`"
  fi
fi

# Installer activity (pkg installs/removals go through installd)
if "${LOG_BIN}" show --predicate 'process == "installd"' --style syslog --last "${LOOKBACK_HOURS}h" \
    > "${SYSEV_DIR}/installer_activity.log" 2>&1; then
  # grep -c prints 0 *and* exits 1 on no match, so `|| echo 0` would yield "0\n0".
  INSTALLED_PKGS=$(grep -c "Installed \"" "${SYSEV_DIR}/installer_activity.log" || true)
  if [[ "${INSTALLED_PKGS:-0}" -gt 0 ]]; then
    warn "${INSTALLED_PKGS} package install(s) in last ${LOOKBACK_HOURS}h (see installer_activity.log)"
    grep "Installed \"" "${SYSEV_DIR}/installer_activity.log" | tail -10 | indent
  else
    ok "No package installs in last ${LOOKBACK_HOURS}h"
  fi
else
  warn "Failed to query installer activity via log show"
fi

# launchd jobs with a non-zero last exit status (unrelated failures often
# explain "it stopped working"). -9 and -15 are launchd stopping idle on-demand
# daemons, which is normal: a lab Mac listed over 100 of them. AutoMDM's own jobs
# keep every status.
FAILED_JOBS=$(launchctl list | awk 'NR>1 && $2 != "0" && $2 != "-" && ($3 ~ /automdm/ || ($2 != "-9" && $2 != "-15")) {print}' || true)
echo "${FAILED_JOBS}" > "${SYSEV_DIR}/launchd_failed_jobs.log"
if [[ -n "${FAILED_JOBS}" ]]; then
  warn "$(printf '%s\n' "${FAILED_JOBS}" | grep -c .) launchd job(s) with a non-zero last exit status, listed in summary.txt"
  echo "${FAILED_JOBS}" | indent
else
  ok "No launchd jobs with a non-zero last exit status"
fi

# Error/fault entries from automdmd processes only. System-wide, this search
# returned over a million lines and took about 2 minutes on a lab Mac.
if "${LOG_BIN}" show --predicate "(messageType == error OR messageType == fault) AND processImagePath BEGINSWITH \"${ORBIT_ROOT}/\"" \
    --style syslog --last "${LOOKBACK_HOURS}h" > "${SYSEV_DIR}/automdmd_errors.log" 2>&1; then
  # Skip the "Timestamp ..." header line log show always prints.
  ERR_COUNT=$(grep -vc '^Timestamp' "${SYSEV_DIR}/automdmd_errors.log" || true)
  if [[ "${ERR_COUNT:-0}" -gt 0 ]]; then
    info "${ERR_COUNT} error/fault-level log line(s) from automdmd processes in last ${LOOKBACK_HOURS}h (see automdmd_errors.log)"
  else
    ok "No error/fault-level log entries from automdmd processes in last ${LOOKBACK_HOURS}h"
  fi
else
  warn "Failed to query unified log for automdmd errors/faults"
fi

# Memory-pressure (jetsam) kills of automdmd processes: macOS' equivalent of the
# Linux OOM killer. The kernel logs these at default level, not as errors.
"${LOG_BIN}" show --predicate 'process == "kernel" AND eventMessage CONTAINS "memorystatus" AND (eventMessage CONTAINS "osquery" OR eventMessage CONTAINS "orbit" OR eventMessage CONTAINS "automdm-desktop")' \
  --style syslog --last "${LOOKBACK_HOURS}h" 2>/dev/null | grep -v '^Timestamp' > "${SYSEV_DIR}/memory_pressure_events.log" || true
if [[ -s "${SYSEV_DIR}/memory_pressure_events.log" ]]; then
  warn "Memory-pressure (jetsam) events naming automdmd processes in last ${LOOKBACK_HOURS}h (see memory_pressure_events.log)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# FINAL RESULT AND ARCHIVE
# ══════════════════════════════════════════════════════════════════════════════
# The result is written before archiving so summary.txt in the archive is
# complete, and nothing writes to WORK_DIR after it's removed.
ARCHIVE_PATH="/tmp/${ARCHIVE_NAME}.tar.gz"

out ""
out "============================================================"
if [[ ${OVERALL_EXIT} -eq 0 ]]; then
  out " Result: ALL CHECKS PASSED"
else
  out " Result: ONE OR MORE CHECKS FAILED — details in summary.txt in the archive"
fi
out "============================================================"

if tar -czf "${ARCHIVE_PATH}" -C "$(dirname "${WORK_DIR}")" "$(basename "${WORK_DIR}")"; then
  rm -rf "${WORK_DIR}"
  # Logs can contain usernames, URLs, and emails: keep the archive private to
  # the admin who ran the script rather than world-readable in /tmp.
  chmod 600 "${ARCHIVE_PATH}"
  [[ -n "${SUDO_USER:-}" ]] && chown "${SUDO_USER}" "${ARCHIVE_PATH}" 2>/dev/null
  echo " Archive: ${ARCHIVE_PATH}"
  echo " Send this file to AutoMDM support."
else
  echo " Archive creation failed; diagnostic data kept at: ${WORK_DIR}" >&2
  OVERALL_EXIT=1
fi
echo "============================================================"

exit ${OVERALL_EXIT}
