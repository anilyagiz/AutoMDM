# AutoMDM Desktop (macOS)

A native macOS application that provides end users with a self-service portal for [AutoMDM](https://automdm.com). It integrates with AutoMDM's [orbit](https://automdm.com/docs/get-started/anatomy#orbit) agent to give users direct access to device management features in a native window instead of a browser.

It also embeds the **AutoMDM Platform SSO (PSSO) extension** (`AutoMDMPSSOExtension.appex`), which implements Apple's Platform Single Sign-On v2 + Password Mode so AutoMDM can create a Mac's local account and keep its password in sync with the user's IdP credentials. See [Platform SSO extension](#platform-sso-extension) below.

> **Heads up — two things named "AutoMDM Desktop":** AutoMDM's agent already ships a tray/menu-bar component called AutoMDM Desktop (bundle ID `com.fleetdm.desktop`, built from `orbit/cmd/desktop`). This is a separate, standalone native app (bundle ID `com.fleetdm.fleet-desktop`) distributed as its own `.pkg`. They use different bundle IDs and can coexist. [Learn more](https://automdm.com/guides/automdm-desktop).

## Features

- **Native macOS app** built with Swift and AppKit
- **Universal binary** supporting Apple Silicon (arm64) and Intel (x86_64)
- **Self-service portal** embedded in a native window via WKWebView
- **Embedded Platform SSO extension** for IdP-based local account creation and password sync
- **Automatic token refresh** handles hourly token rotation transparently
- **Loading screen** with AutoMDM logo while the portal loads
- **File download support** for `.mobileconfig` profiles and other files served by AutoMDM
- **MDM enrollment in a sheet** over the device page, so end users can always get back to it
- **Dark/light mode** respects the user's system appearance
- **`automdm://` URL scheme** for deep linking to Self-service, Policies, triggering refetches, and Update/Install all
- **Works with or without MDM** — reads the AutoMDM URL from the automdmd configuration profile, or from the orbit launchd plist on Macs that aren't MDM-enrolled
- **Code signed and notarized** for secure distribution via `.pkg` installer

## Requirements

- macOS 13.0 (Ventura) or later for the app; the PSSO extension requires macOS 14.0+ (the password-sync feature targets macOS 26+)
- AutoMDM's orbit agent installed and enrolled. MDM enrollment is not required — see [Configuration Sources](#configuration-sources) for where the AutoMDM URL is read from
- The orbit identifier file must exist at `/opt/orbit/identifier`

## Installation

The signed, notarized `.pkg` is produced by CI (see [CI/CD](#cicd)) and uploaded as a workflow artifact. To deploy:

- **Via AutoMDM (Software):** upload the `.pkg` to AutoMDM as a software installer. AutoMDM Desktop will appear in the software catalog for deployment.
- **Manually:** double-click the `.pkg` and follow the installer.

The app is placed in `/Applications` with `root:admin` ownership and `755` permissions. On upgrades, the installer gracefully quits AutoMDM Desktop before installing and automatically relaunches it afterward.

Installing the app into `/Applications` is also what registers the bundled `AutoMDMPSSOExtension.appex` with the system so it becomes selectable by a `com.apple.extensiblesso` configuration profile.

## How It Works

1. **Reads the AutoMDM URL** from the automdmd configuration profile, or from the orbit launchd plist on Macs that aren't MDM-enrolled (see [Configuration Sources](#configuration-sources))
2. **Reads the device token** from `/opt/orbit/identifier` (managed by orbit, rotates hourly)
3. **Opens the self-service portal** at `{AutoMDMURL}/device/{token}/self-service` in an embedded browser window

### Token Rotation

The device token in `/opt/orbit/identifier` rotates every hour. AutoMDM Desktop handles this automatically:

- A background timer checks the identifier file every 60 seconds (and keeps the Dock badge current even when the window is closed)
- On HTTP 401/403 errors or error page detection, the app immediately checks for a new token and retries (up to 3 attempts with 5-second delays)
- Token refreshes are invisible to the user — the page silently reloads with the new token

### MDM enrollment

**Turn on MDM** on the device page opens AutoMDM's enrollment page (`{server}/enroll`) in a sheet over the device page instead of replacing it:

- **Close** (or Esc) dismisses the sheet; the device page stays loaded underneath
- The sheet tightens the page's spacing and sizes itself to the content so every step is visible without scrolling. In a short window it extends below the window, staying on screen
- When end-user authentication is required, the IdP sign-in runs inside the sheet
- After the enrollment profile downloads, the sheet stays open so the remaining steps (installing the profile in System Settings) stay visible

### File Downloads

When AutoMDM serves downloadable content (e.g., MDM enrollment profiles):

- `.mobileconfig` files are downloaded and automatically opened for installation. Outside the enrollment sheet, the app then returns to the device page
- All other file types (`.pkg`, `.dmg`, `.zip`, etc.) are saved to `~/Downloads`

### Security

- App Transport Security (ATS) is enforced for the in-app WebView — the embedded portal requires HTTPS
- External links are restricted to `https`, `http`, and `mailto` schemes
- The transparency link's redirect is resolved by the app and only an absolute destination is opened in the default browser, so the device token never reaches the browser
- During an SSO flow, navigations to a known authenticator app scheme (`com-okta-authenticator`, used by Okta Verify for FastPass) are handed to that app; any other custom scheme ends the flow
- Device tokens are percent-encoded and not exposed in error messages
- Downloaded files are only auto-opened if they are `.mobileconfig` profiles
- The WebView uses a non-persistent data store (no cookies or cache persist between sessions)
- Mutable state is protected by a serial dispatch queue for thread safety

## Platform SSO extension

`AutoMDMPSSOExtension.appex` is an Apple `com.apple.AppSSO.idp-extension` that implements Platform SSO v2 in Password Mode. The AutoMDM server provides the IdP endpoints; the extension registers the device's keys with AutoMDM and proxies password sign-in / key exchange through it.

The extension is bundled inside the app at `AutoMDM Desktop.app/Contents/PlugIns/AutoMDMPSSOExtension.appex` and ships in the same `.pkg`.

### How it binds to an AutoMDM server

The extension derives all of its endpoints from a single `BaseURL` value supplied in the `ExtensionData` dictionary of a `com.apple.extensiblesso` configuration profile (see [`automdm-sso-extension-example.mobileconfig`](./automdm-sso-extension-example.mobileconfig)):

```xml
<key>BaseURL</key> <string>https://automdm.example.com</string>
```

From that it derives, under `/api/mdm/apple/psso/`:

- `POST /nonce`        — single-use nonces for token requests
- `POST /registration` — device key registration
- `POST /token`        — password login / key request / key exchange
- `GET  /jwks`         — AutoMDM's PSSO public keys

The AutoMDM server also serves an Apple App Site Association file at `https://<hostname>/.well-known/apple-app-site-association` containing an `authsrv` entry naming the extension's `<TeamID>.<BundleID>` — i.e. `8VBZ3948LU.com.fleetdm.fleet-desktop.pssoextension`.

Because the same generic, CI-built extension must work against *any* AutoMDM server, the associated domain is **not** baked into the binary. Instead:

- The entitlement `com.apple.developer.associated-domains` ships as an **empty array**, with `com.apple.developer.associated-domains.mdm-managed` set to `true`.
- The actual `authsrv:` domain (the configured AutoMDM server) is delivered at runtime by an MDM **AssociatedDomains** payload targeting the extension's bundle ID.

### Entitlements

Both the host app and the extension carry restricted (Apple-managed) entitlements. These are not freely assertable — `codesign` only honors them when a Developer ID **provisioning profile** that grants them is embedded in the bundle (see [Signing secrets](#signing-secrets)).

| Bundle | Entitlement | Value |
|--------|-------------|-------|
| App + extension | `com.apple.developer.associated-domains` | empty array (must exist) |
| App + extension | `com.apple.developer.associated-domains.mdm-managed` | `true` |
| Extension only | `com.apple.security.app-sandbox` | `true` |
| Extension only | `com.apple.security.network.client` | `true` |

The host app is deliberately **not** sandboxed — it reads `/opt/orbit/identifier` and automdmd's configuration plists outside any container. App extensions are always sandboxed.

## Development

### Project Structure

```
apps/automdm-desktop-macos/
├── AutoMDMDesktop/
│   ├── AutoMDMDesktopApp.swift        # App delegate, main menu, entry point
│   ├── AutoMDMService.swift           # Config reading, token management, refresh timer
│   ├── BrowserWindow.swift          # WKWebView window, loading overlay, downloads
│   ├── EnrollmentSheet.swift        # Sheet that shows AutoMDM's MDM enrollment page
│   ├── Info.plist                   # App bundle metadata
│   ├── AutoMDMDesktop.entitlements     # Host-app entitlements (managed associated domains)
│   ├── AppIcon.icns                 # App icon
│   └── automdm-logo.png               # AutoMDM logo for loading screen
├── AutoMDMPSSOExtension/
│   ├── AuthenticationViewController.swift            # Principal class (SSO request handler)
│   ├── AuthenticationViewController+PSSO.swift        # Registration handler
│   ├── AuthenticationViewController+Shared.swift      # Payload / key-ID / config helpers
│   ├── AuthenticationViewController+Networking.swift  # URLSession against AutoMDM
│   ├── AuthenticationViewController+UserRegistration.swift  # Interactive user registration (sign-in form → AutoMDM /token)
│   ├── PSSOLoginRequest.swift                         # Device-signed login JWS + encrypted password assertion
│   ├── RegistrationFormView.swift                     # Native username/password form
│   ├── Info.plist                                     # appex metadata (NSExtension dict)
│   └── AutoMDMPSSOExtension.entitlements                # Extension entitlements
├── automdm-sso-extension-example.mobileconfig          # Example com.apple.extensiblesso profile
├── build.sh                         # Compiles the universal app + appex
└── build-pkg.sh                     # Creates the .pkg installer
```

The CI workflow lives at [`.github/workflows/automdm-desktop-macos-build.yml`](../../.github/workflows/automdm-desktop-macos-build.yml).

The PSSO extension is built as a Foundation app extension with `swiftc`: there is no `main()`; the entry point is `NSExtensionMain` and the principal class is loaded from the appex `Info.plist`. `swiftc`'s `-module-name` must match the module prefix in `NSExtensionPrincipalClass` (`AutoMDMPSSOExtension`).

### Building Locally

```bash
# Build the app (with the embedded extension)
./build.sh

# Run
open "build/AutoMDM Desktop.app"

# Build the (unsigned) .pkg installer
./build-pkg.sh
```

To test end to end locally (a dev-signed app/extension against your local AutoMDM server), `build.sh` accepts `TEAM_ID`, `APP_BUNDLE_ID`, `EXT_BUNDLE_ID`, `SIGNING_IDENTITY`, `APP_PROFILE`, and `EXT_PROFILE`, and `build-pkg.sh` accepts `APP_BUNDLE_ID` and `INSTALLER_SIGNING_IDENTITY` — together they build and sign the app + `.pkg` under a non-production AutoMDM dev team. See the [Local development: Apple Platform SSO](../../docs/Contributing/guides/platform-sso-local-development.md) guide for the full walkthrough (signing assets, AASA override, tunnel, profile, and logs).

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ORBIT_ROOT_DIR` | `/opt/orbit` | Override the orbit directory (changes where the identifier file is read from) |

### Configuration Sources

| File | Key | Purpose |
|------|-----|---------|
| `/Library/Managed Preferences/com.fleetdm.fleetd.config.plist` | `AutoMDMURL` | AutoMDM server URL delivered via the automdmd MDM configuration profile. Checked first. |
| `/Library/LaunchDaemons/com.fleetdm.orbit.plist` | `EnvironmentVariables.ORBIT_AUTOMDM_URL` | AutoMDM server URL baked in by `automdmctl package --automdm-url`. Used when the managed preference is absent (Macs that aren't MDM-enrolled). |
| `/opt/orbit/identifier` | — | Device authentication token (rotates hourly) |

A scheme-less value such as `automdm.example.com:8080` is treated as HTTPS, matching orbit. If neither plist provides a URL, the app displays an error and quits.

> **Note:** A Mac enrolled with `--use-system-config` whose configuration profile was later removed keeps the URL only in `/opt/orbit/automdm_url.txt`, which only root can read, so the app can't use it. Re-deliver the profile or reinstall automdmd with `--automdm-url`.

### URL Scheme

AutoMDM Desktop registers the `automdm://` URL scheme, allowing other tools and scripts to open specific pages:

| URL | Action |
|-----|--------|
| `automdm://self-service` | Opens the Self-service tab |
| `automdm://software` | Opens the Software tab |
| `automdm://policies` | Opens the Policies tab |
| `automdm://refetch` | Triggers a device refetch and opens the app |
| `automdm://update_all` | Opens Self-service and clicks "Update all" |
| `automdm://install_all` | Opens Self-service and clicks "Install all" |
| `automdm://install_all?category_id=##` | Opens Self-service filtered to a category and clicks "Install all" |
| `automdm://anything-else` | Brings the app to the foreground |

Both `_` and `-` separators are accepted (e.g. `automdm://install_all` and `automdm://install-all` are equivalent).

Example usage from a script or terminal:

```bash
open automdm://self-service
open automdm://refetch
open automdm://install_all
open "automdm://install_all?category_id=5"
```

## CI/CD

[`.github/workflows/automdm-desktop-macos-build.yml`](../../.github/workflows/automdm-desktop-macos-build.yml) runs on pull requests touching `apps/automdm-desktop-macos/**`, on push to `main`, and via manual dispatch. It:

1. Compiles a universal binary (arm64 + x86_64) for the app and the extension, and assembles the `.appex` inside the `.app`
2. Embeds the Developer ID provisioning profiles into the app and extension bundles
3. Code signs **inside-out** — the extension first, then the host app — each with its own entitlements
4. Packages into a `.pkg` installer with a custom distribution XML
5. Signs the `.pkg` with AutoMDM's Developer ID Installer certificate
6. Notarizes with Apple and staples the ticket
7. Uploads the signed `.pkg` as a workflow artifact (retained for 30 days)

The workflow always signs and notarizes. Runs without access to the signing secrets — fork pull requests, or any run before the provisioning profiles have been added — **fail** rather than producing an unsigned artifact.

### Signing secrets

The workflow reuses the Developer ID certificate secrets already used by AutoMDM's other macOS build workflows, plus **two new provisioning-profile secrets** required for the extension's restricted entitlements:

| Secret | Purpose |
|--------|---------|
| `APPLE_APPLICATION_CERTIFICATE` / `..._PASSWORD` | Developer ID Application certificate (.p12, base64) + password |
| `APPLE_INSTALLER_CERTIFICATE` / `..._PASSWORD` | Developer ID Installer certificate (.p12, base64) + password |
| `APPLE_USERNAME` / `APPLE_PASSWORD` | Apple ID + app-specific password for notarization |
| `APPLE_TEAM_ID` | Apple Developer Team ID |
| `KEYCHAIN_PASSWORD` | Temporary CI keychain password |
| `APPLE_AUTOMDM_DESKTOP_APP_PROFILE_B64` | base64 of the Developer ID provisioning profile for `com.fleetdm.fleet-desktop` |
| `APPLE_PSSO_EXT_PROFILE_B64` | base64 of the Developer ID provisioning profile for `com.fleetdm.fleet-desktop.pssoextension` |

The Developer ID certificate identities (SHA-1) are pinned in the workflow `env` block, matching the identities used by AutoMDM's orbit and automdmd-base builds.

#### Provisioning profiles (one-time Apple Developer portal setup)

The `com.apple.developer.associated-domains*` entitlements are Apple-managed: `codesign` will not honor them without a Developer ID provisioning profile that grants them. Profiles are **not committed** — they are team/cert-bound build inputs that expire, so they're stored as the base64 secrets above (the same pattern as the `.p12` certs).

Under AutoMDM's Apple Developer team (`8VBZ3948LU`, the team that owns the pinned Developer ID certificates):

1. Register two App IDs:
   - `com.fleetdm.fleet-desktop` (host app)
   - `com.fleetdm.fleet-desktop.pssoextension` (extension)
2. Enable the **Associated Domains** and **MDM Managed Associated Domains** capabilities on both App IDs.
3. Create a **Developer ID** provisioning profile (distribution, platform macOS) for each App ID. **Select the same Developer ID Application certificate that CI signs with** — SHA-1 `604D877399AAEB7630A78B84F288E2D28A2EDE42` (the identity pinned in the workflow). AutoMDM has more than one "Developer ID Application: Fleet Device Management Inc" certificate; a profile generated against the wrong one will sign and **notarize successfully but get SIGKILLed by AMFI at launch**, because AMFI requires the signing cert to appear in the profile's `DeveloperCertificates`. The `Verify profiles authorize the signing certificate` workflow step guards against this.
4. base64-encode each downloaded `.provisionprofile` and store them as `APPLE_AUTOMDM_DESKTOP_APP_PROFILE_B64` and `APPLE_PSSO_EXT_PROFILE_B64`:
   ```bash
   base64 -i AutoMDMDesktop_DeveloperID.provisionprofile | pbcopy   # → APPLE_AUTOMDM_DESKTOP_APP_PROFILE_B64
   base64 -i AutoMDMPSSOExtension_DeveloperID.provisionprofile | pbcopy  # → APPLE_PSSO_EXT_PROFILE_B64
   ```

Re-encode and update the secrets when a profile expires or the signing certificate is rotated. To inspect a profile — its entitlements and, crucially, the certs it authorizes — dump it with `security cms -D -i <profile>.provisionprofile`; the `DeveloperCertificates` array must contain the CI signing cert above.

## Releasing

[`.github/workflows/release-automdm-desktop-macos.yml`](../../.github/workflows/release-automdm-desktop-macos.yml) publishes a tagged, signed, notarized build to `https://download.fleetdm.com/automdm-desktop-macos/v<version>/`. Releases are immutable — a version that already exists on download.fleetdm.com cannot be overwritten. No GitHub Release is created; the git tag is the release marker.

1. Bump `CFBundleShortVersionString` (and `CFBundleVersion`) in `AutoMDMDesktop/Info.plist` and merge to `main`.
2. Tag the commit and push the tag:
   ```bash
   git tag automdm-desktop-macos-v<version>
   git push origin automdm-desktop-macos-v<version>
   ```
3. In the Actions tab, run **Release AutoMDM Desktop (macOS)**, selecting the tag in the "Use workflow from" dropdown.

The workflow fails before building if the selected ref isn't an `automdm-desktop-macos-v*` tag on `main`, if the tag version doesn't match `Info.plist`, or if that version is already uploaded. It builds via the CI workflow above, uploads the pkg plus a `meta.json` (`version`, `automdm_desktop_pkg_sha256`, `automdm_desktop_pkg_url`), then downloads the pkg back from the public URL and verifies its SHA256 before succeeding. The checksum and URLs are written to the run summary.

The `testing` input uploads to `download-testing.fleetdm.com` instead of production — use it for the first run after changing the workflow.

## License

Licensed under the MIT Expat license via the repository [root LICENSE](../LICENSE).
