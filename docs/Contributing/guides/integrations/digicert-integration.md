# DigiCert integration

## Set up dev environment

- Go to https://demo.one.digicert.com/.
- In Account > Access > Users > `<User name>` > API Tokens, create/retrieve the API token.
- In Trust Lifecycle > Policies > Certificate profiles, create/retrieve the GUID of the profile.

_Notes:_
- Profile enrollment method must be `REST API` and Authentication method must be `3rd Party App`
- To add User Principal Name to the issued certificate, the `Seat type` must be `User`, and `Subject Alternative Name (SAN) > Other name (UPN)` must be set to `REST request` as the source.

## Issues and limitations

- CA name should be treated as a unique identifier and never changed once set. The profiles (and potential renewals) are tied to the CA name. To cleanly change the CA name, remove any profiles using the old CA name (which will remove the associated certificates from devices), change the CA name, upload new profiles using the new CA name.
- Although you can have multiple PKCS12 payloads in a profile, each CA can only be used once per profile.
- Certificates created prior to the renewal feature being added will not be automatically renewed. To make them eligible for automatic renewal, manually resend the DigiCert profile to the host
- Sometimes macOS keychain takes a bit to drop the prior certificate especially if you leave
  Keychain Access open. If the prior certificate is being returned by OSQuery or you see it in
  Keychain Access, most likely quitting Keychain Access and reopening it will fix this

## Architecture diagrams

```mermaid
---
title: Enable DigiCert integration
---
sequenceDiagram
    autonumber
    actor admin as Admin
    participant automdm as AutoMDM server
    participant digicert as DigiCert
    admin->>+automdm: Save configs
    automdm->>automdm: Validate inputs
    automdm->>+digicert: Get profile by GUID
    digicert-->>-automdm: Profile (Active)
    automdm->>automdm: Encrypt API token
    automdm-->>-admin: Done
```

```mermaid
---
title: Deploy DigiCert certificate to Apple host
---
sequenceDiagram
    autonumber
    actor admin as Admin
    participant host as Host
    participant automdm as AutoMDM server
    participant digicert as DigiCert
    participant apple as Apple
    admin->>+automdm: Upload PKCS12 Apple configuration profile
    automdm->>automdm: Validate profile
    automdm-->>-admin: OK

    automdm--)+automdm: Process profiles every 30 seconds
    automdm->>automdm: Validate profile
    automdm->>automdm: Decrypt API token
    automdm->>automdm: Inject AutoMDM variables
    automdm->>+digicert: Get certificate
    digicert-->>-automdm: Certificate
    automdm->>automdm: Save NotValidBefore, NotValidAfter dates, Serial number
    automdm->>+apple: Push notification (APNS)
    apple-->>-automdm: OK
    deactivate automdm

    host--)+automdm: Idle message
    automdm-->>-host: PKCS12 profile (DigiCert certificate)
    activate host
    host->>host: Add certificate to keychain
    host-->>-automdm: Acknowledged message
    activate automdm
    automdm-->>-host: Empty

    host->>+automdm: Read
    automdm-->>-host: Get profiles command (once an hour)
    
    host->>+automdm: Write (profiles)
    automdm->>automdm: PKCS12 profile Verified
    automdm-->>-host: OK
```

```mermaid
---
title: Automatically renew existing DigiCert certificate before it expires 
---
sequenceDiagram
    autonumber
    actor admin as Admin
    participant host as Host
    participant automdm as AutoMDM server
    participant digicert as DigiCert
    participant apple as Apple
    automdm->>automdm: Process cleanups then aggregations cron every hour
    automdm->>automdm: Query Host MDM Managed Certificates table for DigiCert certificates<br> expiring soon(validity window > 30 days and not_valid_after < 30 days or <br>validity window < 30 days and not_valid_after < half validity window from<br>now)
    automdm->>automdm: Mark host-profile association rows for expiring certificates for resend


    automdm--)+automdm: Process profiles every 30 seconds
    automdm->>automdm: Validate profile
    automdm->>automdm: Decrypt API token
    automdm->>automdm: Inject AutoMDM variables
    automdm->>+digicert: Get certificate
    digicert-->>-automdm: Certificate
    automdm->>automdm: Save NotValidBefore, NotValidAfter dates, Serial number
    automdm->>+apple: Push notification (APNS)
    apple-->>-automdm: OK
    deactivate automdm

    host--)+automdm: Idle message
    automdm-->>-host: PKCS12 profile (DigiCert certificate)
    activate host
    host->>host: Add certificate to keychain
    host->>host: Remove prior certificate from keychain<br>(once no longer in use - usually seconds to minutes)
    host-->>-automdm: Acknowledged message
    activate automdm
    automdm-->>-host: Empty

    host->>+automdm: Read
    automdm-->>-host: Get profiles command (once an hour)
    
    host->>+automdm: Write (profiles)
    automdm->>automdm: PKCS12 profile Verified
    automdm-->>-host: OK
```

## Sample PKCS12 profile

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
    <dict>
        <key>PayloadContent</key>
        <array>
            <dict>
                <key>Password</key>
                <string>$AUTOMDM_VAR_DIGICERT_PASSWORD_Test_CA</string>
                <key>PayloadContent</key>
                <data>${AUTOMDM_VAR_DIGICERT_DATA_Test_CA}</data>
                <key>PayloadDisplayName</key>
                <string>CertificatePKCS12</string>
                <key>PayloadIdentifier</key>
                <string>com.fleetdm.pkcs12</string>
                <key>PayloadType</key>
                <string>com.apple.security.pkcs12</string>
                <key>PayloadUUID</key>
                <string>ee86cfcb-2409-42c2-9394-1f8113412e04</string>
                <key>PayloadVersion</key>
                <integer>1</integer>
            </dict>
        </array>
        <key>PayloadDisplayName</key>
        <string>DigiCert profile</string>
        <key>PayloadIdentifier</key>
        <string>TopPayloadIdentifier</string>
        <key>PayloadType</key>
        <string>Configuration</string>
        <key>PayloadUUID</key>
        <string>TopPayloadUUID</string>
        <key>PayloadVersion</key>
        <integer>1</integer>
    </dict>
</plist>
```
