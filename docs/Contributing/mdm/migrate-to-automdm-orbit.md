# Migrate to AutoMDM via Orbit/AutoMDM Desktop

We provide a way to MDM migrate devices enrolled via other MDM solutions to AutoMDM using AutoMDM Desktop (Orbit). 

## Relevant code pieces
- [mdm_migration_darwin.go](../../../orbit/pkg/useraction/mdm_migration_darwin.go) handles the Orbit side showing the migration dialogs and unenrollment checking logic.
- [migrate_mdm endpoint](https://github.com/anilyagiz/automdm/blob/main/ee/server/service/devices.go#L22-L98) handles the AutoMDM server side of the migration request and triggering the unenrollment webhook.

## Prerequisites for the option to show
- The device must have AutoMDM Desktop (Orbit) installed, and be Orbit enrolled into AutoMDM.
- The device must be enrolled in an MDM solution that is not AutoMDM.
- AutoMDM server needs to see and recognize the device being MDM enrolled elsewhere.

> If the device is ADE enrolled, it requires the device to be assigned to AutoMDM in ABM.

## Migration flow
1. User initiates migration on their device via AutoMDM Desktop (Orbit)
2. AutoMDM Desktop (Orbit) checks the locally placed file [`mdm_migration.txt`](https://github.com/anilyagiz/automdm/blob/main/orbit/cmd/desktop/desktop.go#L710-L715) value, to determine what kind of previous MDM enrollment was done.
3. AutoMDM Desktop (Orbit) hits the endpoint `POST /api/_version_/automdm/device/{token}/migrate_mdm` on the AutoMDM server, to notify AutoMDM of the migration request, which will in turn notify the [configured Webhook URL](https://github.com/anilyagiz/automdm/blob/main/ee/server/service/devices.go#L84).
4. AutoMDM Desktop (Orbit) then waits for unenrollment locally to be completed. See [how long it waits](https://github.com/anilyagiz/automdm/blob/main/orbit/pkg/useraction/mdm_migration_darwin.go#L54).
    1. If it successfully unenrolled while waiting, the loading dialog will disappear.
        a. If manual enrollment, then the My Device page will pop up and instruct the user to manually enroll.
        b. If ADE enrollment, then it will close and Orbit will periodically call the `profiles renew -type enrollment` command to trigger the native ADE enrollment flow from Apple.
    2. If it failed to unenroll, the user can trigger the Migrate to AutoMDM flow again, which will keep sending the unenroll webhook until we sucessfully unenroll. _Currently the AutoMDM server limits webhook requests to [every 3 minutes](https://github.com/anilyagiz/automdm/blob/main/server/automdm/mdm.go#L29)_ 

Once the new enrollment steps have been followed, the device should now be MDM enrolled into AutoMDM.
