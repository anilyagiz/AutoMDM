# Windows Autopilot for Beginners

This document teaches how Windows Autopilot enrollment works with AutoMDM, starting from the simplest overview and progressively adding detail. Each level builds on the previous one.

## Level 1: The big picture

Autopilot lets a new Windows device enroll in AutoMDM automatically the first time it boots. Three parties are involved.

```mermaid
sequenceDiagram
    participant MS as Microsoft Cloud
    participant Win as Windows Device
    participant AutoMDM as AutoMDM Server

    Win->>MS: Boot and request deployment profile
    MS-->>Win: Deployment profile (org branding, MDM URL)
    Win->>AutoMDM: Enroll in MDM
    AutoMDM-->>Win: Certificate + management config
```

- The deployment profile tells the device which MDM server to enroll in (AutoMDM) and what the first-boot wizard should look like.
- "Enroll in MDM" is a multi-step exchange (expanded in Level 3).
- An IT admin configures the Microsoft-to-AutoMDM link once before any device boots. That setup is shown next.

## Level 2: Two phases, two humans

Autopilot has a one-time admin setup phase and a per-device enrollment phase. The end user only participates during the first-boot sign-in.

```mermaid
sequenceDiagram
    participant Admin as IT Admin
    participant MS as Microsoft Cloud
    participant Win as Windows Device
    participant AutoMDM as AutoMDM Server
    participant User as End User

    rect rgb(235, 245, 255)
        note over Admin, AutoMDM: One-time admin setup
        Admin->>MS: Register AutoMDM as MDM provider in Entra
        Admin->>MS: Create Autopilot deployment profile in Intune
        Admin->>AutoMDM: Connect AutoMDM to Entra (tenant ID, client ID, client secret)
    end

    rect rgb(255, 245, 235)
        note over Admin, MS: Per-device registration
        Admin->>MS: Upload device hardware hash to Intune
    end

    rect rgb(245, 255, 235)
        note over Win, AutoMDM: Per-device enrollment (OOBE)
        Win->>MS: Boot: contact Autopilot service
        MS-->>Win: Deployment profile (branding, MDM URL)
        User->>Win: Sign in with Entra credentials
        Win->>MS: Join Entra (get MDM discovery URL)
        Win->>AutoMDM: MDM enrollment (discovery, policy, enroll)
        AutoMDM-->>Win: Certificate + management endpoint
    end
```

- **Hardware hash** is a device fingerprint (TPM + SMBIOS data) that lets Microsoft's Autopilot service recognize the device before anyone signs in. It must be registered per device -- either manually by the admin (CSV upload) or by the OEM before shipping (e.g. a company orders laptops from Dell and provides their Entra tenant ID; Dell registers all the hashes into the company's Autopilot before the devices ship).
- **Entra join** means the device registers itself as an object in Entra ID. Entra's Mobility settings tell the device which MDM to enroll in.
- The **MDM discovery URL** points to AutoMDM (e.g. `https://automdm.example.com/api/mdm/microsoft/discovery`).

## Level 3: The enrollment protocol

The "enroll in MDM" box from Level 2 is actually three SOAP requests (the MS-MDE2 protocol). After enrollment, two background processes kick in: AutoMDM's Autopilot cron sync and SyncML management sessions.

```mermaid
sequenceDiagram
    participant User as End User
    participant Win as Windows Device
    participant MS as Microsoft Cloud
    participant AutoMDM as AutoMDM Server
    participant Cron as AutoMDM Cron Job

    User->>Win: Enter Entra credentials at OOBE
    Win->>MS: Authenticate + join Entra
    MS-->>Win: Access token (JWT) + MDM discovery URL

    rect rgb(255, 250, 235)
        note over Win, AutoMDM: MS-MDE2 enrollment protocol
        Win->>AutoMDM: POST /mdm/microsoft/discovery
        AutoMDM-->>Win: Enrollment policy URL, enrollment URL

        Win->>AutoMDM: POST /mdm/microsoft/policy (BinarySecurityToken = Entra JWT)
        AutoMDM-->>Win: Certificate requirements (key length, hash algorithm)

        note left of Win: Device generates keypair + CSR
        Win->>AutoMDM: POST /mdm/microsoft/enroll (CSR, DeviceID, ZTDID)
        note right of AutoMDM: AutoMDM signs CSR with WSTEP identity key
        AutoMDM-->>Win: Signed certificate + management URL
    end

    note over Win, AutoMDM: Device is now MDM-enrolled

    loop SyncML sessions (OMA-DM MD5 digest auth)
        Win->>AutoMDM: POST /mdm/microsoft/management (SyncML)
        AutoMDM-->>Win: Commands + config profiles
    end

    rect rgb(240, 240, 255)
        note over Cron, MS: Background sync (every 5 min)
        Cron->>MS: Graph API: list Autopilot devices
        MS-->>Cron: Device list (serial, ZTDID, group tag)
        Cron->>AutoMDM: Create or refresh pending host records
    end
```

- **BinarySecurityToken:** For Autopilot/Entra-joined devices, this is the Entra JWT. AutoMDM validates it before issuing a certificate.
- **ZTDID (Zero Touch Device ID):** A GUID Microsoft assigns to each Autopilot-registered device. The device sends it during enrollment. AutoMDM stores it and uses it to link the MDM enrollment to the Autopilot record.
- **WSTEP:** The certificate enrollment protocol. AutoMDM's WSTEP identity key signs the device's CSR and returns the client certificate in the provisioning doc. AutoMDM does not use this certificate to authenticate management sessions: SyncML sessions use OMA-DM MD5 digest credentials that AutoMDM provisions at enrollment and rekeys on the first check-in. Certificate renewal is advertised but not implemented yet ([#52492](https://github.com/fleetdm/fleet/issues/52492)), and signed SyncML messages are not verified yet ([#48771](https://github.com/fleetdm/fleet/issues/48771)).
- **Why the cron sync?** AutoMDM already knows about devices that enroll in its MDM server, but the sync serves three purposes that MDM enrollment alone cannot: **(1)** it creates pending host records for devices registered in Autopilot that haven't been unboxed yet, so admins see their full inventory in AutoMDM before a single device boots; **(2)** it stores the Autopilot group tag on the host for display (group tags don't drive team assignment yet; pending hosts land in the Windows enrollment default team); **(3)** it detects when a device is removed from Autopilot in Intune and cleans up the AutoMDM record.
- **Cron sync:** Runs every 5 minutes, calls the Microsoft Graph API, and diffs against AutoMDM's stored Autopilot records.
- **Linking:** During the first SyncML session, AutoMDM links the enrollment to its pending host first by ZTDID (fast, authoritative), then by serial number as a fallback.

## Level 4: Setup experience (ESP)

After enrollment, Windows displays the Enrollment Status Page (ESP) while AutoMDM configures the device. AutoMDM holds the device at the ESP until required software is installed, then releases it to the desktop.

```mermaid
sequenceDiagram
    participant Win as Windows Device (ESP)
    participant AutoMDM as AutoMDM Server
    participant Orbit as Orbit (AutoMDM agent)
    participant Queue as Unified Queue

    note over Win, AutoMDM: Immediately after enrollment
    Win->>AutoMDM: POST /mdm/microsoft/management (first SyncML session)
    AutoMDM-->>Win: ESP hold + install automdmd command
    note right of Win: Device shows "Setting up<br/>your device..."
    note right of Win: Device downloads and<br/>installs automdmd (Orbit)

    note over Orbit, AutoMDM: Orbit comes online
    Orbit->>AutoMDM: POST /automdm/orbit/enroll
    AutoMDM-->>Orbit: orbit_node_key

    Orbit->>AutoMDM: POST /automdm/orbit/config
    AutoMDM-->>Orbit: RunSetupExperience = true

    Orbit->>AutoMDM: POST /automdm/orbit/setup_experience/init
    AutoMDM->>Queue: Enqueue software installers

    loop Every 30s
        Orbit->>AutoMDM: POST /automdm/orbit/setup_experience/status
        AutoMDM-->>Orbit: pending and completed items
        AutoMDM->>Queue: Queue next install step
    end

    note over Queue, Orbit: Software installs delivered via unified queue

    Win->>AutoMDM: Next SyncML check-in
    AutoMDM-->>Win: Release ESP (all items complete)
    note right of Win: User reaches desktop
```

- **ESP hold:** AutoMDM sends DMClient CSP and EnrollmentStatusTracking CSP commands via SyncML to block OOBE progress. These are sent before Orbit has enrolled.
- **How Orbit gets installed:** In the first SyncML session, AutoMDM sends an MDM command telling the device to download and install the automdmd MSI package. Windows executes this while the ESP is displayed. Once installed, Orbit starts as a service and enrolls with AutoMDM's API.
- **Orbit** is AutoMDM's agent. Once installed on the device, it drives the setup experience by polling the server every 30 seconds.
- **Unified queue:** AutoMDM's internal job queue that delivers software installs to Orbit in sequence.
- **3-hour timeout:** If setup hasn't completed after 3 hours, AutoMDM fails the ESP with an error screen instead of releasing. The device offers Reset, plus Continue anyway unless the team requires all software.
- **User-scoped profiles** are held until the device reports a signed-in Entra user via the `LoginStatus` alert, because Windows rejects user-scope writes until a user MDM context exists.

## Glossary

| Term | What it is |
|------|-----------|
| Entra ID | Microsoft's identity service (formerly Azure AD) |
| Intune | Microsoft's device management console; hosts Autopilot deployment profiles |
| OOBE | Out-of-Box Experience: the first-boot setup wizard |
| ESP | Enrollment Status Page: blocks OOBE until configuration is done |
| ZTDID | Zero Touch Device ID: Autopilot device GUID assigned by Microsoft |
| WSTEP | Certificate enrollment protocol (AutoMDM signs the device's CSR) |
| SyncML | XML protocol for MDM command exchange between device and server |
| MD5 digest auth | OMA-DM application-level authentication (username, password, nonce) that AutoMDM uses for SyncML sessions |
| Graph API | Microsoft's REST API; AutoMDM uses it to list Autopilot devices |
| Hardware hash | TPM + SMBIOS fingerprint that uniquely identifies a device to Autopilot |
| CSP | Configuration Service Provider: Windows interface for reading/writing device settings |
| MS-MDE2 | Microsoft's MDM enrollment protocol (discovery, policy, enroll) |

## How this compares to Apple ADE

For team members familiar with Apple's Automated Device Enrollment:

- **Registration portal:** ADE uses Apple Business Manager; Autopilot uses Intune + Entra.
- **Enrollment trigger:** ADE pushes an enrollment profile via APNs; Autopilot uses Entra's Mobility MDM discovery URL.
- **Certificate protocol:** ADE uses SCEP; Autopilot uses WSTEP.
- **Hold during setup:** Both have one. ADE uses `AwaitingConfiguration` + `DeviceConfigured` MDM commands; Autopilot uses ESP hold/release via DMClient CSP.
- **Background sync:** AutoMDM's Autopilot cron (5 min) mirrors the DEP sync cron that pulls device lists from Apple.
- **Management protocol:** ADE uses Apple's MDM protocol over APNs; Autopilot uses OMA-DM SyncML over HTTPS.

## Related resources

- [Windows Autopilot dev testing guide](windows-autopilot.md): Proxmox VMs, sysprep, troubleshooting
- [Windows MDM glossary and protocol](windows-mdm-glossary-and-protocol.md): Protocol details, registry keys, SyncML structure
- [Windows MDM architecture](windows-mdm-architecture.md): SyncML, CSPs, poll schedules
- [Setup experience overview](../../setup-experience/setup-experience-overview.md): Setup experience flow across all platforms
- [Windows MDM setup guide](https://automdm.com/guides/windows-mdm-setup): User-facing setup instructions
- [MS-MDE2 enrollment spec](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-mde2/): Microsoft's enrollment protocol reference
- [Autopilot overview](https://learn.microsoft.com/en-us/autopilot/windows-autopilot): Microsoft's Autopilot documentation
