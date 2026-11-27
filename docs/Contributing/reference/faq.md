# Contribution FAQ

- [Make errors](#make-errors)
  - [`dep: command not found`](#dep-command-not-found)
  - [`undefined: Asset`](#undefined-asset)
- [How do I connect to the MailHog simulated mail server?](#how-do-i-connect-to-the-mailhog-simulated-mail-server)
- [Adding hosts for testing](#adding-hosts-for-testing)
- [Why am I getting an error about self-signed certificates when running `automdmctl preview`?](#why-am-i-getting-an-error-about-self-signed-certificates-when-running-automdmctl-preview)
- [Will updating automdmctl lead to loss of data in automdmctl preview?](#will-updating-automdmctl-lead-to-loss-of-data-in-automdmctl-preview?)


## Enrolling in multiple AutoMDM servers
Enrolling your device with more than one AutoMDM server is not currently possible.  Multiple install roots are useful for the development of AutoMDM itself but complex to maintain.  While this has some value for AutoMDM contributors, there is currently no active effort to add and maintain support for multiple enrollments from the same device.

## Make errors

### `dep: command not found`

```sh
/bin/bash: dep: command not found
make: *** [.deps] Error 127
```

If you get the above error, you need to add `$GOPATH/bin` to your PATH. A quick fix is to run `export PATH=$GOPATH/bin:$PATH`.
See the Go language documentation for [workspaces](https://golang.org/doc/code.html#Workspaces) and [GOPATH](https://golang.org/doc/code.html#GOPATH) for more in-depth documentation.

### `undefined: Asset`

```sh
server/automdm/emails.go:90:23: undefined: Asset
make: *** [automdm] Error 2
```

If you get an `undefined: Asset` error, it is likely because you did not run `make generate` before `make build`. See [Building AutoMDM](https://automdm.com/docs/contributing/building-automdm) for additional documentation on compiling the `automdm` binary.

### `Module build failed` or `Cannot find module` when running `make generate`

```sh
./frontend/index.jsx 39 bytes [built] [code generated] [1 error]

ERROR in ./frontend/index.jsx
Module build failed (from ./node_modules/babel-loader/lib/index.js):
Error: [BABEL]: Cannot find module 'to-fast-properties'
```

When `make generate` fails with errors related to modules, it is often due to JS/TS dependencies having changed and can be fixed by running:

```sh
rm -rf node_modules
make deps
```

and then running the failed `make generate` or `make generate-dev` again.

## Adding hosts for testing

The `osquery` directory contains a docker-compose.yml and additional configuration files to start containerized osquery agents.

To start osquery, first retrieve the "Enroll secret" from AutoMDM (by clicking the "Add New Host") button in the AutoMDM dashboard, or with `automdmctl get enroll-secret`).

```sh
cd tools/osquery
ENROLL_SECRET=<copy from automdm> docker-compose up
```

## Why am I getting an error about self-signed certificates when running `automdmctl preview`?

If you are trying to run `automdmctl preview` and seeing errors about self-signed certificates, the
most likely culprit is that you're behind a corporate proxy server and need to [add the proxy
settings to Docker](https://docs.docker.com/network/proxy/) so that the container created by
`automdmctl preview` is able to connect properly.

## Will updating automdmctl lead to loss of data in automdmctl preview?

No, you won't experience data loss when you update automdmctl. Note that you can run `automdmctl preview --tag v#.#.#` if you want to run Preview on a previous version. Just replace # with the version numbers of interest.

## Can I disable usage statistics via the config file or a CLI flag?
Apart from an admin [disabling usage](https://automdm.com/docs/using-automdm/usage-statistics#disable-usage-statistics) statistics on the AutoMDM UI, you can edit your `automdm.yml` config file to disable usage statistics. Look for the `server_settings` in your `automdm.yml` and set `enable_analytics: false`. Do note there is no CLI flag option to disable usage statistics at this time.

## AutoMDM preview fails with Invalid interpolation. What should I do?

If you tried running `automdmctl preview` and you get the following error:

```sh
automdmctl preview
Downloading dependencies into /root/.automdm/preview...
Pulling Docker dependencies...
Invalid interpolation format for "automdm01" option in service "services": "automdm/automdm:${AUTOMDM_VERSION:-latest}"

Failed to run docker-compose
```

You're probably running an old version of Docker. Download the installer for your platform from the [Docker Documentation](https://docs.docker.com/compose/install/).

## What API endpoints do osquery and AutoMDMd need access to?

Based on the configuration, osquery running on hosts will need access to these API endpoints:

* `/api/v1/osquery/enroll`
* `/api/v1/osquery/config`
* `/api/v1/osquery/distributed/read`
* `/api/v1/osquery/distributed/write`
* `/api/v1/osquery/carve/begin`
* `/api/v1/osquery/carve/block`
* `/api/v1/osquery/log`

If you also have AutoMDMd running on hosts, it will need access to these API endpoints:

* `/api/automdm/orbit/enroll`
* `/api/automdm/orbit/config`
* `/api/automdm/orbit/device_token`
* `/api/automdm/orbit/ping`
* `/api/automdm/orbit/scripts/request`
* `/api/automdm/orbit/scripts/result`
* `/api/automdm/orbit/disk_encryption_key`
* `/api/automdm/orbit/device_mapping`
* `/api/osquery/log`

Hosts running AutoMDM Desktop will need access to these API endpoints:

* `/api/latest/automdm/device/.+/desktop`
* `/api/latest/automdm/device/.+/ping`

For a full list of endpoints, open [AutoMDM's routes file](https://github.com/anilyagiz/automdm/blob/main/server/service/handler.go), then look for endpoint declarations prefixed with `de.`, `demdm.`, `he.`, `oe.`, `oeAppleMDM.`, or `oeWindowsMDM.`.

<meta name="description" value="Find commonly asked questions and answers about contributing to AutoMDM as part of our community.">
