# End user authentication

- [AutoMDM's guide for setting up IdP authentication during macOS setup experience](https://automdm.com/guides/setup-experience#require-idp-authentication)
- On AutoMDM's [pricing page](https://automdm.com/pricing), this feature is called `User account sync` (as of 2025/03/31)
- Also known as: IdP integration

## Set up dev environment

Create a SAML app in an IdP.

### Instructions for Okta

- Create a SAML app in your Okta developer account following this guide: https://automdm.com/docs/deploy/single-sign-on-sso#okta
  - We found that Audience URI needed to be the AutoMDM app URL
- Create user(s) at `Directory > People`
- Assign user(s) to your app at `Applications > Applications > Assign Users to App`
- (Optional) Remove 2FA requirement for you app in `Security > Authentication Policies > Any two factors > Applications > Switch policy`

## Description

If the IT admin configured end user authentication, we change the `configuration_web_url` value in the [enrollment JSON profile](https://developer.apple.com/documentation/devicemanagement/profile) to be `{server_url}/mdm/sso`. This page gets the SAML Request from `{server_url}/api/v1/automdm/mdm/sso` and initiates the SSO flow in the setup assistant web view.

`end_user_authentication` setting is global, but `enable_end_user_authentication` is a team-level setting.

Key points about the flow:

1. The SSO flow ends with a callback to the AutoMDM server which contains information about the user that just logged in. We store this information in the `mdm_idp_accounts` table. Because at this point we don't know from which host UUID the request is coming in, we generate a random UUID as the key to look up this information (stored as `mdm_idp_accounts.uuid`). It is called `enrollment_reference` on the `/mdm/apple/enroll` endpoint.
2. The AutoMDM server responds with an enrollment profile, that contains a special `ServerURL` with a query parameter `enroll_reference` (note the name difference). This parameter has the random UUID generated in step 1. This value is also called `EnrollmentRef` or `EnrollmentReference` in the codebase.
3. During MDM enrollment, we grab the `enroll_reference` parameter, if present, and we try to match it to a host. This allows us to link end user IdP accounts used during enrollment with a host.
4. Before releasing the device from awaiting configuration, we send an [AccountConfiguration command](https://developer.apple.com/documentation/devicemanagement/accountconfigurationcommand/command-data.dictionary) to the host, to pre-set the macOS local account username to the value we got stored in `mdm_idp_accounts`. The command sets the following properties:
  - LockPrimaryAccountInfo=true
  - PrimaryAccountUserName
  - PrimaryAccountFullName
5. During a subsequent osquery device refresh, we lookup the `email` from `mdm_idp_accounts` and save it in `host_emails` table. This email shows up in host details.

## Diagrams

The following diagram focuses on the end user authentication part of the setup experience and skips some of the other details (automdmd download, EULA, etc.).

```mermaid
---
title: End user authentication flow during macOS setup experience
---
sequenceDiagram
    autonumber
    actor Admin
    actor User
    participant automdm as AutoMDM server
    participant host as macOS host
    participant Apple
    participant IdP
    Admin->>IdP: Enable SAML integration
    Admin->>automdm: Enable end user authentication
    Admin->>Apple: Add macOS to ADE
    automdm->>Apple: Update enrollment profile (DEP sync)
    User->>+host: Turn on device
    host->>Apple: Start setup
    host->>host: Open setup assistant webview
    host->>automdm: Redirect to sso endpoint
    activate automdm
    automdm->>+IdP: SAML request (HTTP redirect binding)
    deactivate automdm
    Note left of IdP: Service provider initiated SAML
    User->>IdP: Login to IdP
    IdP->>-automdm: SAML response to sso/callback endpoint (Assertion Consumer Service)
    activate automdm
    automdm->>automdm: Validate SSO session valid and not expired
    automdm->>automdm: Save username and display name
    automdm-->>host: Redirect UI to enroll endpoint
    deactivate automdm
    host->>host: Continue setup experience
    host->>+automdm: MDM TokenUpdate
    deactivate host
    automdm->>-Apple: APNS notify
    activate Apple
    Note right of automdm: MDM job runs every minute
    Apple--)host: Notify
    deactivate Apple
    activate host
    host->>automdm: Get commands
    activate automdm
    automdm-->>host: AccountConfiguration MDM command
    deactivate host
    deactivate automdm
    host->>+automdm: osquery results (mdm server_url, etc.)
    Note right of automdm: Refetch runs every hour
    automdm->>-automdm: Extract enroll_reference from URL and save IdP email
```

## Issues and limitations

- AutoMDM does not support OpenID Connect (OIDC) integration. AutoMDM only supports SAML.
- AutoMDM expects the SAML username to be an email. (2025/04/02: we plan to remove this restriction)
- After initial enrollment, AutoMDM does not sync local macOS account with IdP: [#27695](https://github.com/fleetdm/fleet/issues/27695).
