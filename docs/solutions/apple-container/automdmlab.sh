#!/usr/bin/env bash
# automdmlab.sh - run an AutoMDM lab on a Mac with Apple container (no Docker).
#
#   ./automdmlab.sh setup    One-time: checks, Rosetta, Apple container, DNS, .env, certs
#   ./automdmlab.sh up       Deploy AutoMDM (first time) and wait until it's ready
#   ./automdmlab.sh start    Start an existing deployment
#   ./automdmlab.sh stop     Stop everything (data kept)
#   ./automdmlab.sh status   Show containers and whether AutoMDM is answering
#   ./automdmlab.sh logs     Follow AutoMDM's logs (logs mysql / logs redis also work)
#   ./automdmlab.sh update   Pull the newest AutoMDM image and recreate the AutoMDM container
#   ./automdmlab.sh certs    Regenerate the TLS certificate (new IP address, or renewal)
#   ./automdmlab.sh down     Remove containers (data kept)
#   ./automdmlab.sh reset    Remove containers AND all data (asks first)

set -euo pipefail
cd "$(dirname "$0")"

DNS_DOMAIN="${DNS_DOMAIN:-test}"
NET="automdm-net"
VOLUMES=(automdm-mysql automdm-redis automdm-data automdm-logs automdm-vulndb)
CONTAINERS=(automdm automdm-redis automdm-mysql)
ENV_URL="https://raw.githubusercontent.com/automdm/automdm/refs/heads/main/docs/solutions/docker-compose/env.example"
CONFIG_TOML="$HOME/.config/container/config.toml"

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m ok\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

exists()   { container ls -a 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "$1"; }
running()  { container ls    2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "$1"; }
volume_exists() { container volume ls 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "$1"; }
lan_ip()   { ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "<your-mac-ip>"; }
# `container ls` only succeeds when Apple container's services are running.
ensure_system() { container ls >/dev/null 2>&1 || container system start; }

load_env() {
  [[ -f .env ]] || die ".env not found. Run: ./automdmlab.sh setup"
  set -a; source ./.env; set +a
  AUTOMDM_PORT="${AUTOMDM_SERVER_PORT:-1337}"
  VULNDB_PATH="${AUTOMDM_VULNERABILITIES_DATABASES_PATH:-/vulndb}"
  AUTOMDM_IMAGE="${AUTOMDM_IMAGE:-automdm/automdm:latest}"
  if [[ "${AUTOMDM_SERVER_TLS:-true}" == "true" ]]; then SCHEME="https"; else SCHEME="http"; fi
}

warn_no_tls() {
  if [[ "$SCHEME" == "http" ]]; then
    say "Note: AUTOMDM_SERVER_TLS is off, so AutoMDM serves plain HTTP. automdmctl and host enrollment need HTTPS."
  fi
}

# Set KEY=VALUE in .env, replacing an existing line or appending a new one.
set_env() {
  if grep -q "^$1=" .env; then sed -i '' "s|^$1=.*|$1=$2|" .env; else echo "$1=$2" >> .env; fi
}

# Create a self-signed certificate whose Subject Alternative Names (SANs) cover
# every address hosts might use to reach AutoMDM. TLS clients ignore the CN.
make_cert() {
  local host ip san cnf
  host="$(hostname)"
  ip="$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || true)"
  san="DNS:${host},DNS:localhost,DNS:automdm.${DNS_DOMAIN},IP:127.0.0.1"
  if [[ -n "$ip" ]]; then san="${san},IP:${ip}"; fi
  mkdir -p certs
  cnf="$(mktemp)"
  cat > "$cnf" << EOF
[req]
distinguished_name = dn
x509_extensions    = v3
prompt             = no
[dn]
CN = ${host}
[v3]
subjectAltName   = ${san}
basicConstraints = critical, CA:TRUE
keyUsage         = critical, digitalSignature, keyEncipherment, keyCertSign
extendedKeyUsage = serverAuth
EOF
  openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout certs/automdm.key -out certs/automdm.crt -config "$cnf" 2>/dev/null
  rm -f "$cnf"
}

show_cert() {
  openssl x509 -in certs/automdm.crt -noout -text | grep -A1 "Subject Alternative Name" | tail -1 | sed 's/^ */    Valid for: /'
  openssl x509 -in certs/automdm.crt -noout -enddate | sed 's/^notAfter=/    Expires:   /'
}

# ---------------------------------------------------------------- setup ----
cmd_setup() {
  say "Checking this Mac"
  [[ "$(uname -m)" == "arm64" ]] || die "Apple silicon is required."
  local major; major="$(sw_vers -productVersion | cut -d. -f1)"
  (( major >= 26 )) || die "macOS 26 or later is required (found $(sw_vers -productVersion))."
  ok "Apple silicon, macOS $(sw_vers -productVersion)"

  if arch -x86_64 /usr/bin/true 2>/dev/null; then ok "Rosetta installed"
  else say "Installing Rosetta"; softwareupdate --install-rosetta --agree-to-license; fi

  if command -v container >/dev/null; then ok "Apple container installed"
  else
    say "Apple container is not installed"
    local url; url="$(curl -fsSL https://api.github.com/repos/apple/container/releases/latest \
      | grep -o '"browser_download_url": *"[^"]*signed\.pkg"' | head -1 | cut -d'"' -f4)"
    [[ -n "$url" ]] || die "Couldn't find the installer. Download it from https://github.com/apple/container/releases"
    read -r -p "Download and install $(basename "$url")? [y/N] " a
    [[ "$a" =~ ^[Yy]$ ]] || die "Install Apple container, then re-run setup."
    local tmp; tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    curl -fL "$url" -o "$tmp/container-installer.pkg"
    sudo installer -pkg "$tmp/container-installer.pkg" -target /
    rm -rf "$tmp"; trap - EXIT
  fi
  ensure_system

  say "Configuring local DNS (.$DNS_DOMAIN)"
  if container system dns list 2>/dev/null | grep -qw "$DNS_DOMAIN"; then ok "DNS domain exists"
  else sudo container system dns create "$DNS_DOMAIN"; fi

  mkdir -p "$(dirname "$CONFIG_TOML")"; touch "$CONFIG_TOML"
  if grep -q '^\[dns\]' "$CONFIG_TOML"; then
    grep -q "domain *= *\"$DNS_DOMAIN\"" "$CONFIG_TOML" && ok "config.toml already sets domain" \
      || die "$CONFIG_TOML has a [dns] section without domain = \"$DNS_DOMAIN\". Edit it, then re-run."
  else
    printf '\n[dns]\ndomain = "%s"\n' "$DNS_DOMAIN" >> "$CONFIG_TOML"
    say "Restarting Apple container to apply DNS settings"
    container system stop; container system start
  fi

  say "Preparing .env"
  if [[ -f .env ]]; then ok ".env already exists (left unchanged)"
  else
    curl -fsSL "$ENV_URL" -o .env
    set_env MYSQL_ROOT_PASSWORD "$(openssl rand -hex 16)"
    set_env MYSQL_PASSWORD      "$(openssl rand -hex 16)"
    set_env AUTOMDM_SERVER_PRIVATE_KEY "$(openssl rand -base64 32)"
    set_env AUTOMDM_SERVER_TLS true
    grep -q '^MYSQL_USER=.'        .env || set_env MYSQL_USER automdm
    grep -q '^MYSQL_DATABASE=.'    .env || set_env MYSQL_DATABASE automdm
    grep -q '^AUTOMDM_SERVER_PORT=.' .env || set_env AUTOMDM_SERVER_PORT 1337
    ok "Generated passwords and server key"
  fi

  if [[ -f certs/automdm.crt && -f certs/automdm.key ]]; then
    ok "Certificates exist"
    openssl x509 -in certs/automdm.crt -noout -text | grep -q "Subject Alternative Name" \
      || say "Note: this certificate has no SANs, so hosts can't verify it. Run: ./automdmlab.sh certs"
  else
    say "Creating self-signed certificate"
    make_cert
    show_cert
  fi

  say "Setup complete. Next: ./automdmlab.sh up"
}

# ---------------------------------------------------------------- deploy ---
run_automdm() {
  local automdm_address
  if [[ -n "${AUTOMDM_SERVER_ADDRESS:-}" && "${AUTOMDM_SERVER_ADDRESS}" =~ :[0-9]+$ ]]; then
    automdm_address="${AUTOMDM_SERVER_ADDRESS}"
  else
    automdm_address="${AUTOMDM_SERVER_ADDRESS:-0.0.0.0}:${AUTOMDM_PORT}"
  fi

  container run -d --name automdm --network "$NET" \
    --platform linux/amd64 --rosetta -m 4G -c 2 \
    -p "${AUTOMDM_PORT}:${AUTOMDM_PORT}" \
    -v automdm-data:/automdm -v automdm-logs:/logs -v "automdm-vulndb:${VULNDB_PATH}" \
    -v "$PWD/certs:/certs:ro" \
    -e AUTOMDM_REDIS_ADDRESS="automdm-redis.${DNS_DOMAIN}:6379" \
    -e AUTOMDM_MYSQL_ADDRESS="automdm-mysql.${DNS_DOMAIN}:3306" \
    -e AUTOMDM_MYSQL_DATABASE="$MYSQL_DATABASE" \
    -e AUTOMDM_MYSQL_USERNAME="$MYSQL_USER" \
    -e AUTOMDM_MYSQL_PASSWORD="$MYSQL_PASSWORD" \
    -e AUTOMDM_SERVER_ADDRESS="$automdm_address" \
    -e AUTOMDM_SERVER_TLS="${AUTOMDM_SERVER_TLS:-true}" \
    -e AUTOMDM_SERVER_CERT=/certs/automdm.crt -e AUTOMDM_SERVER_KEY=/certs/automdm.key \
    -e AUTOMDM_SERVER_PRIVATE_KEY="$AUTOMDM_SERVER_PRIVATE_KEY" \
    -e AUTOMDM_LICENSE_KEY="${AUTOMDM_LICENSE_KEY:-}" \
    -e AUTOMDM_LOGGING_JSON="${AUTOMDM_LOGGING_JSON:-true}" \
    -e AUTOMDM_OSQUERY_STATUS_LOG_PLUGIN="${AUTOMDM_OSQUERY_STATUS_LOG_PLUGIN:-filesystem}" \
    -e AUTOMDM_FILESYSTEM_STATUS_LOG_FILE="${AUTOMDM_FILESYSTEM_STATUS_LOG_FILE:-/logs/osqueryd.status.log}" \
    -e AUTOMDM_FILESYSTEM_RESULT_LOG_FILE="${AUTOMDM_FILESYSTEM_RESULT_LOG_FILE:-/logs/osqueryd.results.log}" \
    -e AUTOMDM_VULNERABILITIES_DATABASES_PATH="$VULNDB_PATH" \
    -e AUTOMDM_VULNERABILITIES_CURRENT_INSTANCE_CHECKS="${AUTOMDM_VULNERABILITIES_CURRENT_INSTANCE_CHECKS:-yes}" \
    "$AUTOMDM_IMAGE" sh -c "/usr/bin/automdm prepare db --no-prompt && /usr/bin/automdm serve" >/dev/null
}

wait_for_dbs() {
  local i out
  say "Waiting for MySQL"
  for i in $(seq 1 120); do
    if out="$(container exec automdm-mysql mysql -h 127.0.0.1 -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" \
                -e 'SELECT 1' "$MYSQL_DATABASE" 2>&1)"; then break; fi
    if grep -q "Access denied" <<< "$out"; then
      die "MySQL rejected the password in .env. The automdm-mysql volume was probably created by an earlier deployment with different passwords. Use that deployment's .env, or run ./automdmlab.sh reset."
    fi
    (( i < 120 )) || die "MySQL didn't become ready. Check: ./automdmlab.sh logs mysql"
    sleep 3
  done

  say "Waiting for Redis"
  for i in $(seq 1 60); do
    container exec automdm-redis redis-cli ping 2>/dev/null | grep -q PONG && break
    (( i < 60 )) || die "Redis didn't become ready. Check: ./automdmlab.sh logs redis"
    sleep 2
  done
}

# AutoMDM finds MySQL and Redis by DNS name. Make sure container DNS answers before
# starting AutoMDM, because AutoMDM exits if its first lookup times out.
wait_for_dns() {
  local i
  running automdm-redis || die "MySQL and Redis aren't running. Run: ./automdmlab.sh start"
  say "Checking container DNS"
  for i in $(seq 1 30); do
    container exec automdm-redis getent hosts "automdm-mysql.${DNS_DOMAIN}" >/dev/null 2>&1 && return
    (( i < 30 )) || die "Containers can't resolve automdm-mysql.${DNS_DOMAIN}. Run: container system stop && container system start, then ./automdmlab.sh start"
    sleep 2
  done
}

automdm_healthy() {
  [[ "$(curl -sk --max-time 5 -o /dev/null -w '%{http_code}' "${SCHEME}://127.0.0.1:${AUTOMDM_PORT}/healthz")" == "200" ]]
}

wait_for_team() {
  say "Waiting for AutoMDM (first start can take 5-10 minutes under Rosetta)"
  local i
  for i in $(seq 1 180); do
    if automdm_healthy; then
      ok "AutoMDM is ready"
      echo "    On this Mac:       ${SCHEME}://localhost:${AUTOMDM_PORT}"
      echo "    Server URL to use: ${SCHEME}://$(lan_ip):${AUTOMDM_PORT}"
      echo "    AutoMDM version:     $(container exec automdm /usr/bin/automdm version 2>/dev/null | head -1)"
      warn_no_tls
      return
    fi
    if ! running automdm; then
      echo "--- last 20 lines of AutoMDM's log ---" >&2
      container logs automdm 2>/dev/null | tail -20 >&2 || true
      die "AutoMDM stopped while starting. See the log above."
    fi
    sleep 5
  done
  die "AutoMDM didn't answer within 15 minutes. Check: ./automdmlab.sh logs"
}

cmd_up() {
  load_env; ensure_system
  exists automdm && die "AutoMDM is already deployed. Use ./automdmlab.sh start"
  if volume_exists automdm-mysql; then
    say "Reusing AutoMDM data from a previous deployment (its passwords must match .env)"
  fi

  say "Creating network and volumes"
  container network create "$NET" >/dev/null 2>&1 || true
  local v
  for v in "${VOLUMES[@]}"; do container volume create "$v" >/dev/null 2>&1 || true; done

  say "Starting MySQL and Redis"
  # An earlier `up` that failed before starting AutoMDM leaves these behind.
  local c
  for c in automdm-mysql automdm-redis; do
    container stop "$c" >/dev/null 2>&1 || true
    container rm "$c" >/dev/null 2>&1 || true
  done
  container run -d --name automdm-mysql --network "$NET" \
    -e MYSQL_ROOT_PASSWORD="$MYSQL_ROOT_PASSWORD" -e MYSQL_DATABASE="$MYSQL_DATABASE" \
    -e MYSQL_USER="$MYSQL_USER" -e MYSQL_PASSWORD="$MYSQL_PASSWORD" \
    -v automdm-mysql:/var/lib/mysql mysql:8 >/dev/null
  container run -d --name automdm-redis --network "$NET" \
    -v automdm-redis:/data redis:6 redis-server --appendonly yes >/dev/null

  say "Fixing volume permissions"
  container run --rm --network "$NET" \
    -v automdm-logs:/logs -v automdm-data:/data -v automdm-vulndb:/vulndb \
    alpine:latest sh -c "chown -R 100:101 /logs /data /vulndb"

  wait_for_dbs
  wait_for_dns
  say "Starting AutoMDM"
  run_automdm
  wait_for_team
}

# ------------------------------------------------------------ day-to-day ---
cmd_start() {
  load_env; ensure_system
  exists automdm || die "AutoMDM isn't deployed yet. Run: ./automdmlab.sh up"
  local c
  for c in automdm-mysql automdm-redis; do running "$c" || container start "$c" >/dev/null; done
  wait_for_dbs
  wait_for_dns
  running automdm || container start automdm >/dev/null
  wait_for_team
}

cmd_stop() {
  say "Stopping AutoMDM"
  local c
  for c in "${CONTAINERS[@]}"; do container stop "$c" >/dev/null 2>&1 || true; done
  ok "Stopped (data kept)"
}

cmd_status() {
  load_env
  container ls -a | awk 'NR==1 || /automdm/'
  if automdm_healthy; then ok "AutoMDM is answering at ${SCHEME}://localhost:${AUTOMDM_PORT}"
  else echo "AutoMDM is not answering."; fi
}

cmd_logs() {
  case "${1:-automdm}" in
    mysql) container logs -f automdm-mysql ;;
    redis) container logs -f automdm-redis ;;
    *)     container logs -f automdm ;;
  esac
}

cmd_update() {
  load_env; ensure_system
  say "Pulling $AUTOMDM_IMAGE"
  container image pull --platform linux/amd64 "$AUTOMDM_IMAGE"
  wait_for_dns
  container stop automdm >/dev/null 2>&1 || true
  container rm automdm >/dev/null 2>&1 || true
  run_automdm
  wait_for_team
}

cmd_certs() {
  load_env
  if [[ -f certs/automdm.crt ]]; then
    mv -f certs/automdm.crt certs/automdm.crt.bak
    mv -f certs/automdm.key certs/automdm.key.bak 2>/dev/null || true
    say "Backed up the old certificate to certs/*.bak"
  fi
  say "Creating a new self-signed certificate"
  make_cert
  show_cert
  if exists automdm; then
    ensure_system
    wait_for_dns
    say "Recreating the AutoMDM container with the new certificate"
    container stop automdm >/dev/null 2>&1 || true
    container rm automdm >/dev/null 2>&1 || true
    run_automdm
    wait_for_team
  fi
  say "Rebuild and reinstall agent packages with --automdm-certificate=certs/automdm.crt"
}

cmd_down() {
  ensure_system
  cmd_stop
  local c
  for c in "${CONTAINERS[@]}"; do container rm "$c" >/dev/null 2>&1 || true; done
  ok "Containers removed (data kept). Redeploy with ./automdmlab.sh up"
}

cmd_reset() {
  read -r -p "Delete ALL AutoMDM data (database, logs, settings)? [y/N] " a
  [[ "$a" =~ ^[Yy]$ ]] || { echo "Cancelled."; exit 0; }
  cmd_down
  local v vols left=()
  for v in "${VOLUMES[@]}"; do container volume rm "$v" >/dev/null 2>&1 || true; done
  vols="$(container volume ls | awk 'NR>1 {print $1}')" || die "Couldn't list volumes to confirm the reset. Check: container volume ls"
  for v in "${VOLUMES[@]}"; do
    if grep -qx "$v" <<< "$vols"; then left+=("$v"); fi
  done
  (( ${#left[@]} == 0 )) || die "Couldn't delete ${left[*]}. A container may still be using it. Check: container ls -a"
  ok "All AutoMDM data deleted. Fresh deploy: ./automdmlab.sh up"
}

case "${1:-}" in
  setup)  cmd_setup ;;
  up)     cmd_up ;;
  start)  cmd_start ;;
  stop)   cmd_stop ;;
  status) cmd_status ;;
  logs)   shift; cmd_logs "${1:-automdm}" ;;
  update) cmd_update ;;
  certs)  cmd_certs ;;
  down)   cmd_down ;;
  reset)  cmd_reset ;;
  *)      sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac