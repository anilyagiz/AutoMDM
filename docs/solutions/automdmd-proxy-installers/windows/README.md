# Build a proxy-aware automdmd MSI

This builds a single automdmd MSI that takes proxy settings as install-time properties. It's for Windows hosts that can only reach AutoMDM through a forward proxy.

```
msiexec /i automdmd-proxy.msi /qn AUTOMDM_URL=https://automdm.example.com AUTOMDM_SECRET=<enroll-secret> ORBIT_PROXY_URL=http://proxy.example.internal:3128 OSQUERY_PROXY=proxy.example.internal:3128
```

Files:

- `build-in-container.sh`: runs the build in a throwaway container. Use this one.
- `build-automdmd-proxy-msi.sh`: the build itself. `build-in-container.sh` runs it for you.


## The proxy properties

automdmd needs the proxy set twice because its two components read proxy settings differently. Orbit reads the standard `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` environment variables. osquery ignores those and only uses its own `--proxy_hostname` flag. The stock MSI sets neither.


### Orbit proxy

`ORBIT_PROXY_URL` is the proxy for Orbit, as a full URL with the port, for example `http://proxy.example.internal:3128`. The installer writes it as both `HTTP_PROXY` and `HTTPS_PROXY`.

- Use `http://` even though AutoMDM is HTTPS. The scheme describes the connection to the proxy, not to AutoMDM. A standard forward proxy like Squid accepts plain HTTP on its port and tunnels the HTTPS traffic through it with CONNECT. Only use `https://` if the proxy itself listens with TLS.
- Orbit uses it to reach the AutoMDM server (enrollment, config, check-ins) and `updates.fleetdm.com` (automdmd updates).


### osquery proxy

`OSQUERY_PROXY` is the same proxy for osquery, as host:port with no scheme, for example `proxy.example.internal:3128`. It becomes `--proxy_hostname`.

- Leave off `http://`. osquery expects only the host and port.
- osquery uses it for all of its traffic to the AutoMDM server: enrollment, config, query results and logs.
- osquery has no bypass list. Everything it sends goes through this proxy, and `NO_PROXY` doesn't apply to it.


### Proxy bypass list

`NO_PROXY` lists destinations Orbit should reach directly instead of through the proxy. It's a comma-separated list with no spaces, and it's optional. If you leave it off, the MSI uses `localhost,127.0.0.1`.

- The default is a safety net. Orbit is written in Go, and Go already sends localhost and loopback addresses direct, so most deployments don't need to pass it at all.
- Add an entry only when Orbit has to reach something without the proxy, such as an internal host the proxy can't route to. Entries can be host names (`corp.example.com` covers that name and its subdomains), IP addresses, or CIDR ranges (`10.0.0.0/8`).
- Don't add the AutoMDM server unless the host can reach it directly. Even then, osquery still sends its AutoMDM traffic through the proxy.


### On the proxy side

- The proxy needs to allow CONNECT to the AutoMDM server on 443 and to `updates.fleetdm.com:443`. That's all the test host needed, with scripts, software installs and AutoMDM Desktop off.
- The proxy resolves the AutoMDM server and `updates.fleetdm.com`, so hosts don't need DNS for those names. If you give the proxy as a hostname, hosts do need DNS for that name.


## Install

From an elevated Command Prompt:

```
msiexec /i C:\automdmd-proxy.msi /qn /l*v C:\automdmd-install.log AUTOMDM_URL=https://automdm.example.com AUTOMDM_SECRET=<enroll-secret> ORBIT_PROXY_URL=http://proxy.example.internal:3128 OSQUERY_PROXY=proxy.example.internal:3128
```

`/l*v` writes a verbose install log. Leave it off if you don't need one.

From PowerShell, pass the arguments as one quoted string. Otherwise PowerShell splits a `NO_PROXY` list at the commas:

```
Start-Process msiexec -Wait -ArgumentList '/i C:\automdmd-proxy.msi /qn AUTOMDM_URL=https://automdm.example.com AUTOMDM_SECRET=<enroll-secret> ORBIT_PROXY_URL=http://proxy.example.internal:3128 OSQUERY_PROXY=proxy.example.internal:3128'
```

In Intune, SCCM or another deployment tool, put the same `PROPERTY=value` pairs in the install command line.

To uninstall: `msiexec /x C:\automdmd-proxy.msi /qn`


## What the script does

The script builds a stock MSI with `automdmctl package`, then edits it with msitools:

- `ORBIT_PROXY_URL` and `NO_PROXY` go into the service's own environment, `HKLM\SYSTEM\CurrentControlSet\Services\AutoMDM osquery\Environment`, so Orbit has them from the first service start.
- `OSQUERY_PROXY` is appended to the service arguments as `-- --proxy_hostname=...`, and Orbit passes it through to osqueryd.
- `AUTOMDM_URL` and `AUTOMDM_SECRET` behave the same as in the stock MSI.


## What's been tested

We installed the automdmctl 4.91.1 build on a Windows 11 VM behind Squid, with all other outbound traffic blocked by Windows Firewall. The VM is Windows on ARM, running the x64 automdmd under emulation. Everything went through the proxy: Orbit enrollment, TUF updates, osquery enrollment, detail queries and software inventory.

We also built it with automdmctl 4.92.0. That build passes the script's checks but hasn't been installed.

The sandboxed container build of 4.91.1 produces the same MSI as the tested one, apart from the IDs generated fresh for every build.

New builds bundle whatever Orbit and osquery are on the stable channel (Orbit 1.61.0 as of 2026-09-23). The tested builds had Orbit 1.60.0. Orbit updates itself to stable either way, so after the first update hosts run the same version regardless. To build exactly the tested versions, set `ORBIT_CHANNEL=1.60.0 OSQUERYD_CHANNEL=5.23.1`.

Only plain CONNECT proxying has been tested. SSL-inspecting and authenticated proxies haven't.


## Build

Build the MSI once and deploy it everywhere; the AutoMDM URL, enroll secret and proxy are supplied at install time.


### Where to run it

The build needs an amd64 (x86_64) machine. Apple silicon Macs and Windows on ARM don't work: the WiX tools run under Wine, and Wine segfaults under x86 emulation.

The recommended way runs the whole build inside a throwaway container, so the host's operating system doesn't matter and nothing gets installed on it. You need Docker or Podman:

- Debian or Ubuntu: `sudo apt-get install -y docker.io`
- RHEL, Rocky, Alma, Oracle Linux or Fedora: `sudo dnf install -y podman`, then run the build with `ENGINE=podman`. RHEL-family distros ship Podman rather than Docker.
- Windows (x64) or an Intel Mac: Docker Desktop. On Windows, run the script from a WSL2 shell. This should work the same way, since the build happens inside a Linux container, but it hasn't been tested.

The container build has been run with Docker on Debian 13 and with Podman on Rocky Linux 9 (RHEL-compatible). Both produced passing builds.

You can also run `build-automdmd-proxy-msi.sh` directly, without a container, but only on Debian or Ubuntu: it apt-installs msitools and Docker and runs as root, so use a box you don't mind changing.


### Steps

1. Put both scripts in a directory and make them executable: `chmod +x *.sh`
2. Run the build. Use `sudo` unless your user is in the `docker` group:

   ```
   ./build-in-container.sh
   ```

   With Podman: `ENGINE=podman ./build-in-container.sh`

3. The script ends with a `VERIFY PASS` or `VERIFY FAIL` block. Don't ship a FAIL.
4. The MSI is `./automdmd-proxy-out/automdmd-proxy.msi`.

That runs `build-automdmd-proxy-msi.sh` inside `fleetdm/wix`, the image automdmctl itself uses to build Windows installers (Debian with Wine and WiX). It uses `automdmctl package --native-tooling`, so the container doesn't get the Docker socket and doesn't run privileged. The container is deleted when the build finishes.

The build needs outbound access to GitHub (the automdmctl download), Docker Hub (the `fleetdm/wix` image), `updates.fleetdm.com` (the Orbit and osquery binaries) and the Debian package mirrors (msitools and python3 inside the container). If the build machine is behind a proxy too, set `HTTPS_PROXY`, `HTTP_PROXY` and their lowercase forms in the shell (`build-in-container.sh` passes them into the container), and give Docker or Podman proxy settings so it can pull the image. The build doesn't need any AutoMDM server credentials. The URL and enroll secret baked into the build are placeholders, and the real ones come from msiexec at install time.

To build a different version:

```
AUTOMDM_VER=v4.92.0 ./build-in-container.sh
```

Options, all set as env vars:

- `AUTOMDM_VER`: automdmctl release that builds the stock MSI. Default `v4.91.1`. The script downloads that exact release and ignores any automdmctl on PATH.
- `ORBIT_CHANNEL`, `OSQUERYD_CHANNEL`: `stable` (default), `edge`, or a version such as `1.60.0` or `5.23.1`. A version sets what's bundled and also what the host auto-updates to, until you change it with `update_channels` in agent options.
- `ARCH=arm64`: Windows on ARM build. Untested.
- `EXTRA_PACKAGE_ARGS`: passed straight to `automdmctl package`. Leave AutoMDM Desktop off, because it doesn't use these proxy settings (#49351).
- `OUT_NAME`, `OUT_DIR`: output file name and directory.
- `ENGINE`: `docker` or `podman`. Defaults to Docker if it's installed.


## Newer automdmctl

automdmctl after 4.92.0 moves Orbit's config into the same `Environment` registry value this script writes (#52475, merged to main but not in a release as of 2026-09-22). The script detects that and merges the proxy entries into AutoMDM's value. That path has only been tested against a simulated MSI, so install-test the first build from a newer automdmctl before sending it to anyone.


## Sign the MSI

The MSI is unsigned unless you pass a cert:

```
SIGN_PFX=/path/cert.pfx SIGN_PFX_PASS_FILE=/path/pass.txt ./build-in-container.sh
```

That signs with osslsigncode, using SHA-256 and a DigiCert RFC 3161 timestamp. `SIGN_PFX_PASS_FILE` is a file containing the PFX password, which keeps it off the command line; both files are mounted read-only into the container. It's been tested with a throwaway self-signed cert. On Windows, check the signature with `Get-AuthenticodeSignature .\automdmd-proxy.msi`.

msiexec, Intune and SCCM don't require a signature. If you enforce AppLocker or WDAC, sign the MSI with your own code-signing cert.


## Verify on a host

```
reg query "HKLM\SYSTEM\CurrentControlSet\Services\AutoMDM osquery" /v Environment
sc qc "AutoMDM osquery"
```

`Environment` should list `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`. `BINARY_PATH_NAME` should end with `-- --proxy_hostname="<proxy>"`.

In the proxy's access log, expect CONNECTs to the AutoMDM server and to `updates.fleetdm.com:443`. Orbit's CONNECTs carry a `Go-http-client` user agent. osquery's carry none.


## Limitations and tips

- Unauthenticated proxies only. automdmd has no supported authenticated-proxy path (#35957).
- If the proxy does SSL inspection, build the package with `automdmctl package --automdm-certificate` pointing to a PEM file that contains the proxy's CA. If some traffic bypasses the proxy (`NO_PROXY`), also include the roots for the AutoMDM server and your software installer CDN. osquery, Orbit and AutoMDM Desktop trust only this file for AutoMDM connections. Don't edit `certs.pem`, because Orbit overwrites it with its embedded bundle on startup. Orbit's update checks against `updates.fleetdm.com` still use the Windows trust store, so import the proxy CA into `LocalMachine\Root` too. This is untested.
- `AUTOMDM_SECRET` appears in msiexec and deployment-tool logs, so use a team-scoped enroll secret.
- When you test with outbound traffic blocked, Windows `curl.exe` stalls on its certificate revocation check, because that check goes out directly. Use `curl.exe --ssl-no-revoke -x ...`. automdmd wasn't affected in testing.
