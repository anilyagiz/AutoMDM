# Building AutoMDM
- [Building the code](#building-the-code)
  - [Quickstart](#quickstart)
  - [Clone and build](#clone-and-build)
  - [Details](#details)
- [Development infrastructure](#development-infrastructure)
  - [Starting the local development environment](#starting-the-local-development-environment)
  - [Running AutoMDM using Docker development infrastructure](#running-automdm-using-docker-development-infrastructure)
  - [Customizing service ports](#customizing-service-ports)
  - [Running a second AutoMDM server](#running-a-second-automdm-server)
- [Debugging with Delve debugger](#debugging-with-delve-debugger)

## Building the code

### Quickstart

Install the dependencies as described in the following sections, then go to [Clone and build](#clone-and-build)

#### macOS

Enable the macOS developer tools:

```sh
xcode-select --install
```

Install [Homebrew](https://brew.sh/) to manage dependencies, then:

```sh
brew install git go node yarn
```

#### Ubuntu

Install dependencies:

```sh
sudo apt-get install -y git golang make nodejs npm
sudo npm install -g yarn
# Install nvm to manage node versions (apt very out of date) https://github.com/nvm-sh/nvm#install--update-script
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
# refresh your session before continuing
nvm install v24.10.0
```

#### Windows

To install dependencies, we recommend using [Chocolatey](https://chocolatey.org/install). Always run Chocolatey in Powershell as an Administrator. Assuming your setup does not include any of our requirements, please run:
```sh
choco install nodejs git golang docker make python2 mingw
npm install -g yarn
```

Note: all packages default to the latest versions. To specify a version, place `--version <version-number>` after each package. You may also install all packages manually from their websites if you prefer.

After installing the packages, you must use **Git Bash** to continue with the [next section](#clone-and-build).

If you plan to use [WSL](https://learn.microsoft.com/en-us/windows/wsl/install) on your windows development environment, you can configure Docker to WSL integration by following the steps in [Microsoft's WSL Documentation](https://learn.microsoft.com/en-us/windows/wsl/tutorials/wsl-containers).

### Clone and build

The following commands will clone the repository and build the AutoMDM and automdmctl binaries. When running these commands for the first time, please review the [Details](#details) section below for additional required dependencies.

```sh
git clone https://github.com/anilyagiz/automdm.git
cd automdm
make deps
make generate
make
```

The binaries are now available in `./build/`.

### Details

To set up a working local development environment, you must have the following toolset:

* [Go](https://golang.org/doc/install)
* [Node.js v24.10.0](https://nodejs.org/en/blog/release/v24.10.0) and [Yarn](https://yarnpkg.com/en/docs/install)
  * A specific version of Node.js can be installed using [nvm](https://github.com/nvm-sh/nvm#install--update-script)
* [GNU Make](https://www.gnu.org/software/make/) (probably already installed if you're on macOS/Linux)

Once you have those requirements, check out this [Loom video](https://www.loom.com/share/e7439f058eb44c45af872abe8f8de4a1) that walks through starting up a local development environment for AutoMDM.

For a text-based walkthrough, continue through the following steps:

First, you will need to install AutoMDM's dependencies.

To do this, run the following from the root of the repository:

```sh
make deps
```

Note: If you are using python >= `3.12`, you may have to install `distutils` using pip.

```sh
pip install setuptools
```
or 
```sh
pip3 install setuptools
```

When pulling changes, it may be necessary to re-run `make deps` if a new Go or JavaScript dependency was added.

### Generating the packaged JavaScript

To generate all necessary code (bundling JavaScript into Go, etc.), run the following:

```sh
make generate
```


#### Automatic rebuilding of the JavaScript bundle

Usually, `make generate` takes the JavaScript code, bundles it into a single bundle via Webpack, and inlines that bundle into a generated Go source file so that all of the frontend code can be statically compiled into the binary. When you build the code after running `make generate`, include all of that JavaScript in the binary.

This makes deploying AutoMDM a dream since you only have to worry about a single static binary. If you are working on frontend code, it is likely that you don't want to have to manually re-run `make generate` and `make build` every time you edit JavaScript and CSS in order to see your changes in the browser. Instead of running `make generate` to solve this problem, before you build the AutoMDM binary, run the following command:

```sh
make generate-dev
```

Instead of reading the JavaScript from an inline static bundle compiled within the binary, `make generate-dev` will generate a Go source file which reads the frontend code from disk and run Webpack in "watch mode."

Note that when you run `make generate-dev`, Webpack will be watching the JavaScript files that were used to generate the bundle so that the process will be long-lived. Depending on your personal workflow, you might want to run this in a background terminal window.

After you run `make generate-dev`, run `make build` to build the binary, launch the binary and you'll be able to refresh the browser whenever you edit and save frontend code.

### Compiling the AutoMDM binary

For convenience, AutoMDM includes a Makefile to build the code:

```sh
make
```

It's unnecessary to use Make to build the code, but using Make allows us to account for cross-platform differences more effectively than the `go build` tool when writing automated tooling. Use whichever you prefer.

## Development infrastructure

The following assumes that you already installed  [Docker](https://docs.docker.com/get-docker/) and [Docker Compose](https://docs.docker.com/compose/install/) (installed by default with Docker on macOS and Windows).


### Starting the local development environment

To set up a canonical development environment via Docker, run the following from the root of the repository:

```sh
docker compose up
```

LocalStack and Azurite are behind [Compose profiles](https://docs.docker.com/compose/how-tos/profiles/) and don't start by default. To start them, run `docker compose --profile localstack --profile azurite up` (or set `COMPOSE_PROFILES=localstack,azurite`).

> Note: you can customize the DB Docker image via the environment variables AUTOMDM_MYSQL_IMAGE and AUTOMDM_MYSQL_PLATFORM. For example:
> - MySQL runs on your host's native architecture by default. To force a platform (for example, to emulate amd64 on Apple silicon), set AUTOMDM_MYSQL_PLATFORM=linux/amd64.
> - To test with MariaDB, set AUTOMDM_MYSQL_IMAGE to mariadb:10.6 or the like (note MariaDB is not officially supported).

### Stopping the local development environment

If you'd like to shut down the virtual infrastructure created by Docker, run the following from the root of the repository:

```sh
docker compose down
```

### Setting up the database tables

Once you `docker compose up` and are running the databases, you can build the code and run the following command to create the database tables:

```sh
./build/automdm prepare db --dev
```

### Running AutoMDM using Docker development infrastructure

To start the AutoMDM server backed by the Docker development infrastructure, run the AutoMDM binary as follows:

```sh
./build/automdm serve --dev
```

> To develop AutoMDM's MDM features, you have to pass a `--automdm_server_private_key` in your `automdm serve --dev` command. Learn how to generate a private key in [AutoMDM's server configuration documentation](https://automdm.com/docs/configuration/automdm-server-configuration#server-private-key).

### Customizing service ports

All Docker service ports in `docker-compose.yml` can be overridden with environment variables. This is useful for avoiding port
conflicts or running multiple independent AutoMDM instances. The defaults match the original hardcoded values, so existing setups
are unaffected.

| Service | Environment variable(s) | Default |
|---|---|---|
| mysql | `AUTOMDM_MYSQL_PORT` | 3306 |
| mysql_test | `AUTOMDM_MYSQL_TEST_PORT` | 3307 |
| mysql_replica_test | `AUTOMDM_MYSQL_REPLICA_TEST_PORT` | 3310 |
| redis | `AUTOMDM_REDIS_PORT` | 6379 |
| s3 (RustFS) | `AUTOMDM_S3_PORT`, `AUTOMDM_S3_CONSOLE_PORT` | 9000, 9001 |
| mailhog | `AUTOMDM_MAILHOG_WEB_PORT`, `AUTOMDM_MAILHOG_SMTP_PORT` | 8025, 1025 |
| mailpit | `AUTOMDM_MAILPIT_WEB_PORT`, `AUTOMDM_MAILPIT_SMTP_PORT` | 8026, 1026 |
| smtp4dev_test | `AUTOMDM_SMTP4DEV_WEB_PORT`, `AUTOMDM_SMTP4DEV_SMTP_PORT` | 8028, 1027 |
| saml_idp | `AUTOMDM_SAML_IDP_HTTP_PORT`, `AUTOMDM_SAML_IDP_HTTPS_PORT` | 9080, 9443 |
| localstack | `AUTOMDM_LOCALSTACK_PORT`, `AUTOMDM_LOCALSTACK_LEGACY_PORT` | 4566, 4571 |
| prometheus | `AUTOMDM_PROMETHEUS_PORT` | 9090 |

You can verify resolved ports with:

```sh
docker compose config
```

Or with custom overrides:

```sh
AUTOMDM_MYSQL_PORT=3326 docker compose config
```

### Running a second AutoMDM server

You can run a fully independent second AutoMDM instance alongside the first by using a separate Docker Compose project with
different ports. This is useful for parallelizing work (e.g. one instance for development, another for QA).

```sh
# Set ports for the second instance
export AUTOMDM_MYSQL_PORT=3326
export AUTOMDM_REDIS_PORT=6389
export AUTOMDM_S3_PORT=9020
export AUTOMDM_S3_CONSOLE_PORT=9011
export AUTOMDM_SERVER_PORT=8090
export AUTOMDM_S3_CARVES_ENDPOINT_URL=http://localhost:$AUTOMDM_S3_PORT
export AUTOMDM_S3_SOFTWARE_INSTALLERS_ENDPOINT_URL=http://localhost:$AUTOMDM_S3_PORT

# Start containers with a separate compose project
docker compose -p automdm-alt up -d mysql redis s3

# Migrate the database
./build/automdm prepare db \
  --mysql_address=localhost:$AUTOMDM_MYSQL_PORT \
  --mysql_username=root \
  --mysql_password=toor \
  --mysql_database=automdm

# Start the AutoMDM server
./build/automdm serve \
  --dev --dev_license \
  --server_address=localhost:$AUTOMDM_SERVER_PORT \
  --mysql_address=localhost:$AUTOMDM_MYSQL_PORT \
  --mysql_username=automdm \
  --mysql_password=insecure \
  --mysql_database=automdm \
  --redis_address=localhost:$AUTOMDM_REDIS_PORT \
  --logging_debug
```

The `-p automdm-alt` flag gives the second instance separate containers and volumes, so data is fully isolated.

> **Important:** Both instances run on `localhost`, so browser session cookies are shared between them. Use an incognito
> window or a different browser profile for the second instance to avoid getting logged out when switching between them.

> **Note:** You would typically run the second instance from a separate clone or git worktree (e.g. `~/automdm2`),
> which also lets you build and run different code versions.

### Developing the AutoMDM UI

When the AutoMDM server is running, the AutoMDM UI is accessible by default at
[https://localhost:8080](https://localhost:8080).

> Note that `./build/automdm serve --dev` requires the use of `make generate-dev` because the server will not use bundled assets in this mode. (You may see an error mentioning a template not found when visiting the website otherwise.)

By default, AutoMDM will try to connect to servers running on default ports on `localhost`. Depending on your browser's settings, you may have to click through a security warning.

The AutoMDM UI is developed with [Typescript](https://www.typescriptlang.org/) using the [React library](https://reactjs.org/docs/getting-started.html) and [SCSS](https://sass-lang.com/) for styling.
The source code can be found in the [frontend](https://github.com/anilyagiz/automdm/tree/main/frontend) directory.

## Debugging with Delve debugger

The [Delve](https://github.com/go-delve/delve) Go debugger can be used for debugging the AutoMDM binary.

Use the following command in place of `make` and `./build/automdm serve --dev`:

```sh
dlv debug --build-flags '-tags=full' ./cmd/automdm -- serve --dev
```

It is important to pass the `-tags=full` build flag; otherwise, the server will not have access to the asset files.

### Attaching a debugger to a running server

You can also run delve in headless mode, which allows you to attach your preferred debugger client and reuse the same session without having to restart the server:

```sh
dlv debug --build-flags '-tags=full' --headless \
  --api-version=2 --accept-multiclient --continue \
  --listen=127.0.0.1:61179 ./cmd/automdm -- serve --dev
```

- If you're using Visual Studio Code, there's a launch configuration in the repo.
- If you're using vim with `vimspector`, you can use the following config:

```json
{
  "configurations": {
    "Go: Attach to AutoMDM server": {
      "adapter": "multi-session",
      "variables": {
        "port": 61179,
        "host": "127.0.0.1"
      },
      "configuration": {
        "request": "attach",
        "mode": "remote"
      }
    }
  }
}
```

<meta name="pageOrderInSection" value="100">
<meta name="description" value="Learn about building AutoMDM from code, development infrastructure, and database migrations.">
