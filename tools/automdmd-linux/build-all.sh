#!/bin/bash

set -euo pipefail

script_dir=$(dirname -- "$(readlink -f -- "$BASH_SOURCE")")
cd "$script_dir"

AUTOMDM_URL=${AUTOMDM_URL:-https://host.docker.internal:8080}

using_localhost() {
    local url=$1
    if [[ "$url" = "https://localhost:"* || "$url" = "https://127.0.0.1:"* || "$url" = "https://host.docker.internal:"* ]]; then
        return 0 # true (exit code 0)
    else
        return 1 # false (exit code 1)
    fi
}

USE_LOCALHOST_AUTOMDM_SERVER_CERTIFICATE=
if using_localhost "$AUTOMDM_URL"; then
    USE_LOCALHOST_AUTOMDM_SERVER_CERTIFICATE=1
fi

echo "Building automdmd deb (amd64) package..."
automdmctl package --type=deb \
	--enable-scripts \
	--automdm-url="$AUTOMDM_URL" \
	--enroll-secret=placeholder \
	${USE_LOCALHOST_AUTOMDM_SERVER_CERTIFICATE:+--automdm-certificate=../osquery/automdm.crt} \
	--disable-open-folder \
	--outfile=automdm-osquery_amd64.deb \
    ${UPDATE_URL:+--update-url=$UPDATE_URL} \
    ${UPDATES_ROOT:+--update-roots="$UPDATES_ROOT"} \
    ${ORBIT_CHANNEL:+--orbit-channel=$ORBIT_CHANNEL} \
    ${DESKTOP_CHANNEL:+--desktop-channel=$DESKTOP_CHANNEL} \
    ${OSQUERYD_CHANNEL:+--osqueryd-channel=$OSQUERYD_CHANNEL} \
	--debug

echo "Building automdmd rpm (amd64) package..."
automdmctl package --type=rpm \
	--enable-scripts \
	--automdm-url="$AUTOMDM_URL" \
	--enroll-secret=placeholder \
	${USE_LOCALHOST_AUTOMDM_SERVER_CERTIFICATE:+--automdm-certificate=../osquery/automdm.crt} \
	--disable-open-folder \
	--outfile=automdm-osquery_amd64.rpm \
    ${UPDATE_URL:+--update-url=$UPDATE_URL} \
    ${UPDATES_ROOT:+--update-roots="$UPDATES_ROOT"} \
    ${ORBIT_CHANNEL:+--orbit-channel=$ORBIT_CHANNEL} \
    ${DESKTOP_CHANNEL:+--desktop-channel=$DESKTOP_CHANNEL} \
    ${OSQUERYD_CHANNEL:+--osqueryd-channel=$OSQUERYD_CHANNEL} \
	--debug

echo "Building automdmd pkg.tar.zst (amd64) package..."
automdmctl package --type=pkg.tar.zst \
	--enable-scripts \
	--automdm-url="$AUTOMDM_URL" \
	--enroll-secret=placeholder \
	${USE_LOCALHOST_AUTOMDM_SERVER_CERTIFICATE:+--automdm-certificate=../osquery/automdm.crt} \
	--disable-open-folder \
	--outfile=automdm-osquery_amd64.pkg.tar.zst \
    ${UPDATE_URL:+--update-url=$UPDATE_URL} \
    ${UPDATES_ROOT:+--update-roots="$UPDATES_ROOT"} \
    ${ORBIT_CHANNEL:+--orbit-channel=$ORBIT_CHANNEL} \
    ${DESKTOP_CHANNEL:+--desktop-channel=$DESKTOP_CHANNEL} \
    ${OSQUERYD_CHANNEL:+--osqueryd-channel=$OSQUERYD_CHANNEL} \
	--debug

echo "Building docker images..."
# BuildKit is required for running "RUN --mount".
export DOCKER_BUILDKIT=1
docker build -t automdmd-ubuntu-24.04 --platform=linux/amd64 -f ./ubuntu-24.04/Dockerfile .
docker build -t automdmd-ubuntu-20.04 --platform=linux/amd64 -f ./ubuntu-20.04/Dockerfile .
docker build -t automdmd-fedora-43 --platform=linux/amd64 -f ./fedora-43/Dockerfile .
docker build -t automdmd-debian-13.4 --platform=linux/amd64 -f ./debian-13.4/Dockerfile .
docker build -t automdmd-cachyos --platform=linux/amd64 -f ./cachyos/Dockerfile .
