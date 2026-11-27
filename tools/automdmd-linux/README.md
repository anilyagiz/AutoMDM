# automdmd-linux

This directory contains scripts to build and run Docker Linux images with automdmd installed on them that connect to an AutoMDM instance running on the host (similar to [tools/osquery](../osquery/)).

## Build automdmd docker images

To build all docker images run:
```sh
./tools/automdmd-linux/build-all.sh
```
To build all docker images using `edge` channels from our staging TUF:
```sh
UPDATE_URL=https://updates-staging.fleetdm.com ORBIT_CHANNEL=edge DESKTOP_CHANNEL=edge OSQUERYD_CHANNEL=edge ./tools/automdmd-linux/build-all.sh
```
To build all docker images using a local TUF repository (e.g. one created with [tools/tuf/test](../tuf/test/)):
```sh
UPDATE_URL=http://host.docker.internal:8081 UPDATES_ROOT=$(automdmctl updates roots --path ./test_tuf) ./tools/automdmd-linux/build-all.sh
```
By default packages point to `https://host.docker.internal:8080` and use the local dev TLS certificate. To point them to a different AutoMDM server (no `--automdm-certificate` is set for non-local URLs):
```sh
AUTOMDM_URL=https://automdm.example.com ./tools/automdmd-linux/build-all.sh
```

## Run automdmd containers

To run all automdmd docker images and enroll them to your local AutoMDM instance, run:
```sh
ENROLL_SECRET=<...> docker compose -f ./tools/automdmd-linux/docker-compose.yml up
```