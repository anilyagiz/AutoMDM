# Android

## Reference links
- [Android Management API](https://developers.google.com/android/management/reference/rest)
- [Google Cloud Pub/Sub API](https://cloud.google.com/pubsub/docs/reference/rest)
- [Google Cloud console pub/sub topics](https://console.cloud.google.com/cloudpubsub/topic/list)

## Using Android MDM for development

By default, AutoMDM server uses automdm.com proxy to access the Google Android Management API. However, the proxy limits each AutoMDM server URL to 1 Android enterprise. This means that you should turn off Android MDM before wiping your development DB.

You can also setup a local proxy or use the Google API directly.

> Android MDM can't be turned on with localhost server URL (e.g. https://localhost:8412), so your server can receive `STATUS_REPORT` via PubSub notifications. Use ngrok or a similar solution to expose your local server to the public internet.

## Setup a Google project and service account

> Instead of creating your own, you can use the shared Android DEV Google cloud project. The credentials for the Android DEV google cloud project can be found in [1Password](https://start.1password.com/open/i?a=N3F7LHAKQ5G3JPFPX234EC4ZDQ&v=3ycqkai6naxhqsylmsos6vairu&i=lsq2qqbfjlrh6eyby2tr2qc7nu&h=automdmdevicemanagement.1password.com). When enabling Android MDM in the Google signup flow, log in with the `@1mdm.xyz` account (credentials are in 1Password).

> **Do not use a personal `@gmail.com` account**. Personal accounts typically do not have the quota to enroll devices. You also cannot use your "automdm.com" one.

Create a Google service account with the following Roles
- Android Management User
- Pub/Sub Admin

To do so:
* Follow instructions at https://developers.google.com/android/management/service-account to create the project and service account
* Follow instructions at https://developers.google.com/android/management/notifications to create pub/sub notifications
* Troubleshooting: watch the video of Gabe and Victor discussion post-standup: https://us-65885.app.gong.io/call?id=4731209913082368849 (starting at the 12:50 timestamp)

## Run an Android MDM environment with a local proxy

If you're hosting your own AutoMDM website instance, you need to set the following in `website/config/custom.js`:

```javascript
  // Android proxy
  androidEnterpriseProjectId: 'yourProject-123456',
  androidEnterpriseServiceAccountEmailAddress: 'yourUser@yourProject-123456.iam.gserviceaccount.com',
  androidEnterpriseServiceAccountPrivateKey: '-----BEGIN PRIVATE KEY-----\nDATA\n-----END PRIVATE KEY-----\n',
```

And also set the following env var for your AutoMDM server:

```bash
export AUTOMDM_DEV_ANDROID_PROXY_ENDPOINT=https://public.url.of.your.website/api/endpoint/
```

Then, launch the website and your AutoMDM server.

## Run an Android MDM environment connected directly to Google

Set the following env vars:

```bash
export AUTOMDM_DEV_ANDROID_GOOGLE_CLIENT=1
export AUTOMDM_DEV_ANDROID_GOOGLE_SERVICE_CREDENTIALS=$(cat credentials.json)
```

To turn on Android MDM, use a Chrome private window (so that you are not logged in with your "automdm.com" address). This is only required to enable Android MDM, you can use a normal window for the rest. In "Settings -> Integrations -> MDM -> Turn On Android -> Connect", use a Google Workspace or Cloud Identity account (not an "automdm.com" one). Select "Sign-up for Android only". Domain name is not important ("test.com" for example). No need to fill anything in the "Data protection officer" and "EU representative" sections, just check the checkbox.

If it fails enabling Android MDM due to an already existing enterprise (error "This enterprise is already enrolled with another EMM." when attempting to enable it again) and a personal (gmail) account was used, you must go to https://play.google.com/work, click "Admin settings", and delete the organization that was created the last time (e.g. "test.com"). You will then be able to enable Android MDM again.

There's also a command-line tool in `tools/android` that can list/delete/etc. enterprises associated with the service account.

## Known issues and limitations
- The AutoMDM server URL must be public for pub/sub to work properly.
- The AutoMDM server URL cannot change -- pub/sub is set up with one URL. See issue [Allow AutoMDM server URL update when using Android](https://github.com/fleetdm/fleet/issues/29878)
- Network reliability issues may leave the Android enterprise flow in a broken state. For example, if automdm.com proxy creates an Android enterprise but AutoMDM server goes offline and does not receive the secret key.

## Physical device or emulator?

Both a physical android device or an emulator will work to enroll into your local automdm server. When connecting a BYOD you must ensure that the device has the ability to create profiles. If you wish to use an emulator the easiest way to do so is to download the [Android studio](https://developer.android.com/studio). When creating an emulator, use a **Google Play** system image (not "Google APIs" or AOSP). Only Google Play images include the full Google Mobile Services stack needed for Android Enterprise enrollment.

If enrollment fails with `DPM_PRECONDITION_CHECK_FAILED_FOR_PROFILE_OWNER`, one thing to check for is an existing work profile from a previous enrollment attempt:
```bash
adb shell pm list users                     # Look for a "Work profile" user
adb shell pm remove-user <id of profile>    # Remove the leftover work profile
```

## Architecture diagrams

```mermaid
---
title: Enable Android MDM
---
sequenceDiagram
    autonumber
    actor Admin
    participant AutoMDM server
    participant automdm.com
    participant Google

    Admin->>+AutoMDM server: Enable Android
    AutoMDM server->>+automdm.com: Get signup url
    automdm.com->>+Google: Get signup url
    Google-->>-automdm.com: Signup url
    automdm.com-->>-AutoMDM server: Signup url
    AutoMDM server->>-Admin: UI redirect (new page)

    Admin->>Google: Enterprise signup
    activate Google
    Google->>AutoMDM server: Signup callback
    deactivate Google
    activate AutoMDM server
    AutoMDM server->>+automdm.com: Create enterprise/policy/pubsub
    automdm.com->>+Google: Create enterprise/policy/pubsub
    Google-->>-automdm.com: Created
    automdm.com-->>-AutoMDM server: Created
    AutoMDM server->>Admin: Self-closing HTML page
    AutoMDM server--)Admin: Android enabled (SSE)
    deactivate AutoMDM server
```

```mermaid
---
title: Enroll BYOD Android device
---
sequenceDiagram
    autonumber
    actor Admin
    actor Employee
    participant Enroll page
    participant AutoMDM server
    participant automdm.com
    participant Google

    Admin->>+AutoMDM server: Get signup link
    AutoMDM server-->>-Admin: Signup link

    Admin->>Employee: Email signup link
    Employee->>+AutoMDM server: Click signup link
    AutoMDM server-->>-Enroll page: HTML page
    Employee->>+Enroll page: Click enroll
    Enroll page->>+AutoMDM server: Get enroll token
    AutoMDM server->>+automdm.com: Get enroll token
    automdm.com->>+Google: Get enroll token
    Google-->>-automdm.com: Enroll token
    automdm.com-->>-AutoMDM server: Enroll token
    AutoMDM server-->>-Enroll page: Enroll token
    Enroll page->>-Employee: Redirect to enroll flow

    Employee->>+Google: Enroll device
    Google-->>Employee: Device enrolled
    Google--)AutoMDM server: Pub/Sub push: ENROLLMENT
    Google--)-AutoMDM server: Pub/Sub push: STATUS_REPORT

    Admin->>+AutoMDM server: Get hosts
    AutoMDM server-->>-Admin: Hosts (including Android)
```

```mermaid
---
title: Partial class diagram
config:
  class:
    hideEmptyMembersBox: true
---
classDiagram
    direction LR
    class `android.Service`
    <<interface>> `android.Service`
    class `android/service.Service`
    `android/service.Service` ..|> `android.Service`: implements

    class `automdm.AndroidDatastore`
    <<interface>> `automdm.AndroidDatastore`
    class `automdm.Datastore`
    <<interface>> `automdm.Datastore`
    class `android.Datastore`
    <<interface>> `android.Datastore`
    `android/service.Service` *-- `automdm.AndroidDatastore`: uses
    `automdm.Datastore` *-- `automdm.AndroidDatastore`: contains
    `mysql.Datastore` ..|> `automdm.Datastore`: implements
    `automdm.AndroidDatastore` *-- `android.Datastore`: contains
    `mysql.Datastore` *-- `android.Datastore`: contains
    `android/mysql.Datastore` ..|> `android.Datastore`: implements
```

## Security and authentication

Android enterprise signup callback is authenticated by a token in the callback URL. The token is created by AutoMDM server.

Getting the Android device enrollment token is authenticated with the AutoMDM enroll secret.

Pub/sub push callback is authenticated by a `token` query parameter. This token is created by AutoMDM server. As of June 2025, this token cannot be easily rotated. We could add another level of authentication where the AutoMDM server would need to check with Google to authenticate the pub/sub message:
- [Authentication for push subscriptions](https://cloud.google.com/pubsub/docs/authenticate-push-subscriptions)
