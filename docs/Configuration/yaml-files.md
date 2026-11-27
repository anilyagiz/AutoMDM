# GitOps

In AutoMDM, you can manage your devices as code.  This section of the docs is a reference for how to do that.

Quick start: [install automdmctl](https://automdm.com/guides/automdmctl#installing-automdmctl) and run `automdmctl new` to generate a starter repository. [Learn how](https://github.com/anilyagiz/automdm/blob/main/cmd/automdmctl/automdmctl/templates/new/README.md). 

> Want to get hands-on?  We run [free GitOps workshops globally](https://automdm.com/gitops-workshop) where you can get certified.

## custom_host_vitals

[Custom host vitals](https://automdm.com/guides/custom-host-vitals) are global and can only be specified inline in your `default.yml` file. They cannot be specified in `teams/team-name.yml` or `teams/unassigned.yml`.

- `name` specifies the vital's name. Must be unique across all custom host vitals.

Each vital is assigned an ID by AutoMDM when it's created, which isn't set in YAML. Find it in the Custom host vitals table in the AutoMDM UI to reference the vital as `$AUTOMDM_HOST_VITAL_<id>` in scripts and configuration profiles, or as `custom_host_vital_id` in a [Host vitals label](#labels)'s `criteria`.

> `custom_host_vitals` is an optional key, but unlike `labels`, omitting it entirely deletes every existing custom host vital. To keep existing vitals, list them.
>
> Removing an entry deletes that vital on the next GitOps run. A run fails if the vital is still referenced by a script, configuration profile, or Host vitals label. Remove the reference first.

### Example

`default.yml`

```yaml
custom_host_vitals:
  - name: Asset tag
  - name: Function
  - name: ITAM device ID
```

## labels

Labels can be specified in your `default.yml` and `teams/team-name.yml` files using inline configuration or references to separate files in your `lib/` folder. Labels cannot be specified in `teams/unassigned.yml`.

Labels support `path:` (single file) and `paths:` (glob pattern) references. See [`path:` vs `paths:`](#path-vs-paths-glob-patterns) for details. Filenames must not contain `*`, `?`, `[`, or `{` when using `path:`.

- `name` specifies the label's name. Must be unique across all global and team labels, and 255 characters or fewer.
    + Changing a label's `name` in GitOps will delete and re-create the label, temporarily clearing its membership. To avoid this, update the label name in the UI before making the change in YAML. 
- `description` specifies the label's description. Must be 255 characters or fewer.
- `platform` specifies platforms for the label to target. Provides an additional filter. Choices for platform are `darwin`, `windows`, `linux`, `ubuntu`, and `centos`. The `linux` option targets hosts on any Linux distribution. All platforms are included by default and this option is represented by an empty string. Only supported if `label_membership_type` is `dynamic`.
- `label_membership_type` specifies label type which determines how hosts are added to the label. Choices for type are `dynamic` , `manual`, and `host_vitals` (default: `dynamic`).
- `query` is the query in SQL syntax used to filter the hosts. Only supported if `label_membership_type` is `dynamic`.
- `hosts` is a list of host identifiers (`id`, `hardware_serial`, or `uuid`). The label will apply to any host with a matching identifier. Only supported if `label_membership_type` is `manual`. If omitted, existing host membership is preserved (no changes). If provided but empty, all hosts are removed from the label.
- `criteria` is the criteria for adding hosts to a host vitals label. Hosts with `vital` data matching the specified `value` will be added to the label. To match on a [custom host vital](https://automdm.com/guides/custom-host-vitals), set `vital` to `custom_host_vital` and also specify `custom_host_vital_id`. See [criteria](https://automdm.com/docs/rest-api/rest-api#criteria) documentation for details.

Only one of `query`, `hosts`, or `criteria` can be specified. If none are specified, a manual label with no hosts will be created.

The `hostname` host identifier is deprecated. Please use a host's `id`, `hardware_serial`, or `uuid` instead.

> `labels` is an optional key. Its behavior depends on the labels exception in **Settings** > **Integrations** > **Change management**.
>
> When the labels exception is disabled, labels are managed in git. If `labels` is included in `default.yml`, existing global labels not listed will be deleted. If included in `teams/team-name.yml`, the team's existing labels not listed will be deleted. Omitting the `labels` key also deletes that file's existing labels. The AutoMDM UI prevents creating and editing labels while [GitOps mode](https://automdm.com/learn-more-about/ui-gitops-mode) is enabled.
>
> When the labels exception is enabled, labels are managed outside of git. `automdmctl gitops` leaves existing labels intact, and fails if a YAML file includes a `labels` key. The AutoMDM UI allows creating and editing labels, even while GitOps mode is enabled. Learn more in the [GitOps mode guide](https://automdm.com/guides/gitops-mode).
>
> Any labels referenced in other sections (like [policies](https://automdm.com/docs/configuration/yaml-files#policies), [reports](https://automdm.com/docs/configuration/yaml-files#reports) or [software](https://automdm.com/docs/configuration/yaml-files#software)) _must_ be specified in the `labels` section.

### Example

#### Inline

`default.yml`

```yaml
labels:
  - name: Arm64
    platform: darwin
    description: macOS hosts on the Arm64 architecture
    query: "SELECT 1 FROM system_info WHERE cpu_type LIKE 'arm64%' OR cpu_type LIKE 'aarch64%'"
    label_membership_type: dynamic
  - name: C-Suite
    description: Hosts belonging to the C-Suite
    label_membership_type: manual
    hosts:
      - "IR7M6ZGQJM"
      - "JMFWY8VZ09"
  - name: Engineering department
    description: Hosts used by engineers
    label_membership_type: host_vitals
    criteria:
      vital: end_user_idp_department
      value: Engineering
  - name: Point of sale terminals
    description: Hosts whose "Function" custom host vital is set to "Point of sale"
    label_membership_type: host_vitals
    criteria:
      vital: custom_host_vital
      custom_host_vital_id: 2
      value: Point of sale
```

#### Separate file

`lib/labels-name.labels.yml`

```yaml
- name: Arm64
  description: Hosts on the Arm64 architecture
  query: SELECT 1 FROM system_info WHERE cpu_type LIKE "arm64%" OR cpu_type LIKE "aarch64%"
  label_membership_type: dynamic
- name: C-Suite
  description: Hosts belonging to the C-Suite
  label_membership_type: manual
  hosts:
      - "IR7M6ZGQJM"
      - "JMFWY8VZ09"
  - name: Engineering department
    description: Hosts used by engineers
    label_membership_type: host_vitals
    criteria:
      vital: end_user_idp_department
      value: Engineering
```

`default.yml`

```yaml
labels:
  - path: ./lib/labels-name.labels.yml
  - paths: ./lib/labels/*.yml
```


## policies

Policies can be specified inline in your `default.yml`, `teams/team-name.yml`, or `teams/unassigned.yml` files. They can also be specified in separate files in your `lib/` folder.

Policies support `path:` (single file) and `paths:` (glob pattern) references. See [`path:` vs `paths:`](#path-vs-paths-glob-patterns) for details. Filenames must not contain `*`, `?`, `[`, or `{` when using `path:`.

### Options

For available options, see the parameters for the [Create policy](https://automdm.com/docs/rest-api/rest-api#create-policy) and [Create team policy](https://automdm.com/docs/rest-api/rest-api#create-team-policy) API endpoints.

#### Patch policy

_Available in AutoMDM Premium_

You can create a patch policy by setting `type` to `patch` and specifying `automdm_maintained_app_slug`.

A patch policy's `query` automatically updates. Hosts will fail this policy if they're not running the latest version found in [the app's metadata](https://github.com/anilyagiz/automdm/tree/main/ee/maintained-apps/outputs). If `version` is set for `automdm_maintained_apps`, that version is included in the query.

To force patch the app when this policy fails, whether or not the app is open, set `install_software` to `true`.

To silently patch the app when this policy fails and app is not open, set both `install_software` and `patch_when_closed to `true`. 

To notify the end user before the app is patched, set `install_software` and `notify_before_patching` to `true`. AutoMDM checks if the app is open, and if yes shows a notification listing the apps that will be updated, waits 1 hour, then installs the patch. A reminder is shown 5 minutes before the install. This option is only available on macOS, and requires the AutoMDM Desktop app (available as an AutoMDM-maintained app).

To patch the app at a scheduled time, set `install_software` and `patch_deadline` to `true`. If the app isn't open, AutoMDM patches it right away. If the app is open, AutoMDM waits for the next deadline in the team's [`patch_deadline_settings`](#patch-deadline-settings). 1 hour before the deadline, AutoMDM shows the end user a notification listing the apps that will close and update. A reminder is shown 5 minutes before the install. If the notification can't be shown (for example, the host is offline or the screen is locked), AutoMDM shows it as soon as it can and installs 1 hour later. This option is only available on macOS, and requires the AutoMDM Desktop app (available as an AutoMDM-maintained app).

Only one of `patch_when_closed`, `notify_before_patching`, or `patch_deadline` can be set to `true`.

AutoMDM adds a read-only pre-install query that checks if the app is open when `patch_when_closed`, `notify_before_patching`, or `patch_deadline` is set to `true`. Also, `continuous_automations_enabled` is automatically set to `true` when one of these options is enabled. The AutoMDM-managed pre-install query is ignored for self-service, host details page, and setup experience installs.

#### Automations

##### Install software

_Available in AutoMDM Premium_

To trigger software install, when policy fails, specify one of:
  - `install_software.package_path` is the path to a [custom package YAML file](#packages) with one package in it. If the file has multiple packages, use `install_software.hash_sha256` instead, or split the multi-package file into single-package files.
  - `install_software.automdm_maintained_app_slug` is an [AutoMDM-maintained app slug](https://automdm.com/docs/configuration/yaml-files#automdm-maintained-apps).
  - `install_software.hash_sha256` is [SHA256 hash](https://automdm.com/docs/configuration/yaml-files#hash) of a custom package.
  - `install_software.app_store_id` is the ID of the Apple App Store app or Google Play app.

##### Run script

_Available in AutoMDM Premium_

To trigger script run, when policy fails, specify:

- `run_script.path` is a path to a script YAML.

> Specifying one package without a list is deprecated as of AutoMDM 4.73. It is maintained for backwards compatibility. Please use a list instead even if you're only specifying one package.

##### Resend configuration profile

_Available in AutoMDM Premium_

To trigger a configuration profile resend when a policy fails, specify:

- `resend_configuration_profile` is the name of the configuration profile to resend. The profile must already be added to the same team's **Controls > OS settings > Configuration profiles** section.

> When the configuration profile automation is added or changed, the policy's status will reset for associated hosts. This allows the resend to trigger on hosts that had previously failed the policy.

##### Example

##### Inline

`default.yml`, `teams/team-name.yml`, or `teams/unassigned.yml`

```yaml
policies:
  - name: macOS - Enable FileVault
    description: This policy checks if FileVault (disk encryption) is enabled.
    resolution: As an IT admin, turn on disk encryption in AutoMDM.
    query: "SELECT 1 FROM filevault_status WHERE status = 'FileVault is On.';"
    platform: darwin
    critical: false
    calendar_events_enabled: false
    conditional_access_enabled: true
    hidden: false
    labels_include_any:
      - Engineering
      - Customer Support
```

##### Separate file

`lib/policies-name.policies.yml`

```yaml
- name: macOS - Enable FileVault
  description: This policy checks if FileVault (disk encryption) is enabled.
  resolution: As an IT admin, turn on disk encryption in AutoMDM.
  query: "SELECT 1 FROM filevault_status WHERE status = 'FileVault is On.';"
  platform: darwin
  critical: false
  calendar_events_enabled: false
  conditional_access_enabled: true
  hidden: false
  resend_configuration_profile: "Passcode requirements"
- name: Windows - Antivirus is healthy
  description: This policy checks that Windows Security Center reports antivirus protection as healthy.
  resolution: Turn on Microsoft Defender Antivirus or another antivirus app, and keep it up to date.
  query: "SELECT 1 FROM windows_security_center WHERE antivirus = 'Good';"
  platform: windows
  critical: false
  conditional_access_enabled: true
- name: macOS - Disable guest account
  description: This policy checks if the guest account is disabled.
  resolution: As an IT admin, deploy a macOS, login window profile with the DisableGuestAccount option set to true.
  query: "SELECT 1 FROM managed_policies WHERE domain='com.apple.mcx' AND username = '' AND name='DisableGuestAccount' AND CAST(value AS INT) = 1;"
  platform: darwin
  critical: false
  calendar_events_enabled: false
  run_script:
    path: ./disable-guest-account.sh
- name: macOS - Firefox installed
  platform: darwin
  description: This policy checks that Firefox is installed.
  resolution: Install Firefox app if not installed.
  query: "SELECT 1 FROM apps WHERE bundle_identifier = 'org.mozilla.firefox'"
  continuous_automations_enabled: true
  install_software:
    package_path: ./firefox.package.yml
- name: macOS - Logic Pro installed
  platform: darwin
  description: This policy checks that Logic Pro is installed
  resolution: Install Logic Pro App Store app if not installed
  query: "SELECT 1 FROM apps WHERE bundle_identifier = 'com.apple.logic10'"
  install_software:
    app_store_id: "1487937127" # (for App Store apps)
- name: macOS - Zoom installed
  platform: darwin
  description: This policy checks that Zoom is installed
  resolution: Install Logic Pro App Store app if not installed
  query: "SELECT 1 FROM apps WHERE bundle_identifier = 'us.zoom.xos'"
  install_software:
    automdm_maintained_app_slug: zoom/darwin
    package_path: ./linux-firefox.deb.package.yml
    # app_store_id: "1487937127" (for App Store apps)
- name: Zoom up to date
  description: Outdated software might introduce security vulnerabilities or compatibility issues.
  resolution: Install the latest version from self-service.
  type: patch
  automdm_maintained_app_slug: zoom/darwin
  continuous_automations_enabled: true
  install_software: true
  patch_when_closed: true
- name: Slack
  description: Outdated software might introduce security vulnerabilities or compatibility issues.
  resolution: Install the latest version from self-service.
  type: patch
  automdm_maintained_app_slug: slack/darwin
  continuous_automations_enabled: true
  install_software: true
- name: 1Password up to date
  description: Outdated software might introduce security vulnerabilities or compatibility issues.
  resolution: Install the latest version from self-service.
  type: patch
  automdm_maintained_app_slug: 1password/darwin
  install_software: true
  notify_before_patching: true
- name: Google Chrome up to date
  description: Outdated software might introduce security vulnerabilities or compatibility issues.
  resolution: Install the latest version from self-service.
  type: patch
  automdm_maintained_app_slug: google-chrome/darwin
  install_software: true
  patch_deadline: true
```

`default.yml` (for policies that neither install software nor run scripts), `teams/team-name.yml`, or `automdm/unassigned.yml`

```yaml
policies:
  - path: ../lib/policies-name.policies.yml
  - paths: ../lib/*.policies.yml
```

> Currently, the `run_script` and `install_software` policy automations can only be configured for a team (`teams/team-name.yml`) or "Unassigned" (`teams/unassigned.yml`). The automations can only be added to policies in which the script (or software) is defined in the same team (or "Unassigned"). `calendar_events_enabled` can only be configured for policies on a team.

> Currently, the `resend_configuration_profile` policy automation can only be configured for a team (`teams/team-name.yml`) or "Unassigned" (`teams/unassigned.yml`). The profile must be defined in the same team's controls section.

> If using `labels_include_any`/`labels_exclude_any` for targeting, these keys are specified on the individual policies. Specifying at the top level of `policies` will _not_ apply the labels to each policy.


## reports

Reports can be specified inline in your `default.yml` file or `teams/team-name.yml` files. They can also be specified in separate files in your `lib/` folder.

Reports support `path:` (single file) and `paths:` (glob pattern) references. See [`path:` vs `paths:`](#path-vs-paths-glob-patterns) for details. Filenames must not contain `*`, `?`, `[`, or `{` when using `path:`.

### Options

For possible options, see the parameters for the [Create report API endpoint](https://automdm.com/docs/rest-api/rest-api#create-report).

### Example

#### Inline

`default.yml` or `teams/team-name.yml`

```yaml
reports:
  - name: Collect failed login attempts
    description: Lists the users at least one failed login attempt and timestamp of failed login. Number of failed login attempts reset to zero after a user successfully logs in.
    query: SELECT users.username, account_policy_data.failed_login_count, account_policy_data.failed_login_timestamp FROM users INNER JOIN account_policy_data using (uid) WHERE account_policy_data.failed_login_count > 0;
    platform: darwin,linux,windows
    interval: 300
    observer_can_run: false
    automations_enabled: false
    labels_include_any:
      - Engineering
      - Customer Support
```

#### Separate file

`lib/reports-name.reports.yml`

```yaml
- name: Collect failed login attempts
  description: Lists the users at least one failed login attempt and timestamp of failed login. Number of failed login attempts reset to zero after a user successfully logs in.
  query: SELECT users.username, account_policy_data.failed_login_count, account_policy_data.failed_login_timestamp FROM users INNER JOIN account_policy_data using (uid) WHERE account_policy_data.failed_login_count > 0;
  platform: darwin,linux,windows
  interval: 300
  observer_can_run: false
  automations_enabled: false
- name: Collect USB devices
  description: Collects the USB devices that are currently connected to macOS and Linux hosts.
  query: SELECT model, vendor FROM usb_devices;
  platform: darwin,linux
  interval: 300
  observer_can_run: true
  automations_enabled: false
```

`default.yml` or `teams/team-name.yml`

```yaml
reports:
  - path: ../lib/reports-name.reports.yml
    labels_include_any:
      - Engineering
      - Customer Support
  - paths: ../lib/*.reports.yml
```

## agent_options

Agent options can be specified inline in your `default.yml` file or `teams/team-name.yml` files. They can also be specified in separate files in your `lib/` folder.

See "[Agent configuration](https://automdm.com/docs/configuration/agent-configuration)" to find all possible options.

### Example

#### Inline

`default.yml` or `teams/team-name.yml`

```yaml
agent_options:
  config:
    decorators:
      load:
        - SELECT uuid AS host_uuid FROM system_info;
        - SELECT hostname AS hostname FROM system_info;
    options:
      disable_distributed: false
      distributed_interval: 10
      distributed_plugin: tls
      distributed_tls_max_attempts: 3
      logger_tls_endpoint: /api/osquery/log
      logger_tls_period: 10
      pack_delimiter: /
```

#### Separate file

`lib/agent-options.yml`

```yaml
config:
  decorators:
    load:
      - SELECT uuid AS host_uuid FROM system_info;
      - SELECT hostname AS hostname FROM system_info;
  options:
    disable_distributed: false
    distributed_interval: 10
    distributed_plugin: tls
    distributed_tls_max_attempts: 3
    logger_tls_endpoint: /api/osquery/log
    logger_tls_period: 10
    pack_delimiter: /
```

`default.yml` or `teams/team-name.yml`

> We want `-` for policies and reports because it’s an array. Agent Options we do not use `-` for `path`.

```yaml
agent_options:
  path: ../lib/agent-options.yml
```

## controls

The `controls` section allows you to configure scripts and device management (MDM) features in AutoMDM.

- `scripts` is a list of paths to macOS, Windows, or Linux scripts. Supports `path:` (single file) and `paths:` (glob pattern, filtered to `.sh`, `.py`, and `.ps1` files only). Filenames must not contain `*`, `?`, `[`, or `{` when using `path:`. See [`path:` vs `paths:`](#path-vs-paths-glob-patterns) for details.
- `windows_enabled_and_configured` specifies whether or not to turn on Windows MDM features (default: `false`). Can only be configured for "All teams" (`default.yml`).
- `windows_entra_tenant_ids` is a list of Microsoft Entra tenant IDs to enable automatic (Autopilot) and manual enrollment by end users (**Settings** > **Accounts** > **Access work or school** on Windows). Not needed to enroll hosts with AutoMDM's agent (automdmd). Can only be configured for "All teams" (`default.yml`). Find your **Tenant ID**, on [**Microsoft Entra ID** > **Home**](https://entra.microsoft.com/#home).
- `windows_entra_client_ids` is a list of Microsoft Entra application (client) IDs for the applications used to enroll Windows hosts via Microsoft Entra. Set this when you set up Entra enrollment: Microsoft Entra issues v2 access tokens whose audience is the application's client ID, so AutoMDM needs the client ID to authorize enrollment. Can only be configured for "All teams" (`default.yml`). Find your **Application (client) ID** on [**Microsoft Entra ID** > **App registrations**](https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade) > your MDM application > **Overview**.
- `enable_turn_on_windows_mdm_manually` specifies whether or not to require end users to sign in using **Settings > Access work or school** (default: `false`). If `false`, MDM is automatically turned on for all Windows hosts that aren't connected to any MDM solution. Either method results in an MDM status of "On (manual)". To get a status of "On (company-owned)", use [Windows Autopilot](https://automdm.com/guides/windows-mdm-setup#windows-autopilot). Can only be configured for "All teams" (`default.yml`). _Available in AutoMDM Premium._
- `windows_migration_enabled` specifies whether or not to automatically migrate Windows hosts connected to another MDM solution. If `false`, MDM is only turned on after hosts are unenrolled from your old MDM solution. `enable_turn_on_windows_mdm_manually` must be set to `false`. (default: `false`). Can only be configured for "All teams" (`default.yml`). _Available in AutoMDM Premium._
- `apple_require_hardware_attestation` specifies whether or not to require Apple hosts with supported hardware (Apple Silicon Macs, and iPhones and iPads with an A11 Bionic or later chip running iOS/iPadOS 16 or later) to complete a device attestation challenge verifying that the hardware serial matches a known host record from AB as part of DEP enrollment (default: `false`). Hosts without supported hardware (for example, Intel Macs) enroll with SCEP and aren't attested, unless `only_allow_apple_business_enrollment` is also `true`. In that case, they can't enroll. Can only be configured for "All teams" (default.yml). _Available in AutoMDM Premium._
- `only_allow_apple_business_enrollment` specifies whether or not to allow only Apple hosts that are assigned to AutoMDM in Apple Business (AB) to turn on MDM, through Automated Device Enrollment (ADE) (default: `false`). When `true`, manual enrollment, over-the-air (OTA) enrollment, and account-driven user enrollment (BYOD) are blocked. AutoMDM also stops renewing MDM certificates for enrolled hosts that aren't in AB, so those hosts lose MDM when their certificate expires. If `apple_require_hardware_attestation` is also `true`, new enrollment is ACME-only: hosts without supported hardware can't enroll (SCEP isn't used as a fallback), and enrolled hosts without supported hardware also stop renewing. Can only be configured for "All teams" (`default.yml`). _Available in AutoMDM Premium._
- `enable_recovery_lock_password` specifies whether or not to enforce Recovery Lock password on eligible macOS hosts (default: `false`). _Available in AutoMDM Premium._
- `enable_profile_validation` specifies whether or not to check that recognized settings in Apple and Windows configuration profiles have values of the expected type when profiles are added or edited (default: `true`). Settings and payload types AutoMDM doesn't recognize are always allowed. Set to `false` to skip this check. Can only be configured for "All teams" (`default.yml`).
- `name_template` sets a naming convention for macOS, iOS, and iPadOS hosts. AutoMDM resolves the template per host, renames the host on the device via an MDM command, and updates the host's name in AutoMDM. Supports the built-in host identity variables (`$AUTOMDM_VAR_HOST_HARDWARE_SERIAL`, `$AUTOMDM_VAR_HOST_UUID`, `$AUTOMDM_VAR_HOST_PLATFORM`), the IdP end-user variables (`$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME`, `_USERNAME_LOCAL_PART`, `_GROUPS`, `_DEPARTMENT`, `_FULL_NAME`), and custom (`$AUTOMDM_SECRET_*`) variables; certificate authority variables aren't supported. A referenced custom variable must already exist. Supported for teams and for hosts that aren't in a team ("Unassigned"): set it in a team's YAML, or in `no_team.yml`/`default.yml` controls to apply it to "Unassigned" hosts. Removing the key clears the template but doesn't rename any host. If a host is renamed on the device, or the device rejects the rename, AutoMDM tries again automatically, up to 3 times, before marking the host as failed. AutoMDM detects a rename the next time the host reports its name (about every hour). On macOS, this requires automdmd to be installed and running. _Available in AutoMDM Premium._

> `enable_disk_encryption` at this level is deprecated. Please use per-platform (`apple_settings`, `windows_settings`, `linux_settings`) instead.
> `windows_require_bitlocker_pin` at this level is deprecated. Please use `windows_settings.require_bitlocker_pin` instead.


#### Example

```yaml
controls:
  scripts:
    - path: ../lib/macos-script.sh
    - path: ../lib/windows-script.ps1
    - path: ../lib/linux-script.sh
    - paths: ../lib/scripts/*.sh  # Glob pattern (filtered to .sh, .py, and .ps1 only)
  windows_enabled_and_configured: true
  windows_entra_tenant_ids:
    - 4e342a0d-ec1a-4353-bdeb-785542e0a8fb
  windows_entra_client_ids:
    - 8c8e3fd4-9b2c-4d3e-8f10-2233445566aa
  enable_turn_on_windows_mdm_manually: false
  windows_migration_enabled: true
  apple_require_hardware_attestation: false
  only_allow_apple_business_enrollment: false
  enable_profile_validation: true
  enable_recovery_lock_password: true
  name_template: "iPad $AUTOMDM_VAR_HOST_HARDWARE_SERIAL"
  android_enabled_and_configured: true
  macos_updates:
    # Custom version
    minimum_version: "15.4.1"
    deadline: "2025-07-01"
    # OR — Latest version (based on host hardware)
    minimum_version: "latest"
    deadline_days: 14
    update_new_hosts: true
  ios_updates:
    # Custom version
    minimum_version: "18.1"
    deadline: "2024-12-31"
    # OR — Latest version (based on host hardware)
    minimum_version: "latest"
    deadline_days: 14
  ipados_updates:
    # Custom version
    minimum_version: "18.1"
    deadline: "2024-12-31"
    # OR — Latest version (based on host hardware)
    minimum_version: "latest"
    deadline_days: 14
  windows_updates:
    deadline_days: 5
    grace_period_days: 2
  apple_settings:
    enable_disk_encryption: true
    enable_escrow_disk_encryption_key: true
    configuration_profiles:
      - paths: ../lib/macos/profiles/*.mobileconfig
        self_service: true
      - paths: ../lib/macos/profiles/*.mobileconfig2
        self_service: false
        hidden: true
      - path: ../lib/macos/profiles/my-declaration.json
          name: Passcode Settings
          description: Enforces passcode requirements for macOS hosts
      - paths: ../lib/macos/profiles/ddm.json
        labels_include_any:
          - Engineering
        activation: ../lib/macos/activations/activation.json
    assets:
      - path: ../lib/macos/assets/my-asset.json  
    end_user_local_account_type: "admin"
  windows_settings:
    configuration_profiles:
      - paths: ../lib/windows/profiles/*.xml
        labels_include_any:
          - Engineering
      - path: ../lib/windows/profiles/background-task.xml
        hidden: true
      - path:  ../lib/windows/profiles/win-firewall.xml
          name: Windows Firewall
          description: Configures firewall rules
    enable_disk_encryption: true
    require_bitlocker_pin: true
  linux_settings:
    enable_escrow_disk_encryption_key: true
    enable_managed_local_account: true   
  android_settings:
    configuration_profiles:
      - path: ../lib/android-profile.json
    certificates:
      - name: wifi-certificate
        certificate_authority_name: EST_WIFI
        subject_name: CN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME, OU=$AUTOMDM_VAR_HOST_UUID, ST=$AUTOMDM_VAR_HOST_HARDWARE_SERIAL
        subject_alternative_name: "DNS=example.com, UPN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"
  setup_experience:
    macos_bootstrap_package: https://example.org/bootstrap_package.pkg
    macos_enable_bootstrap_package_during_manual_enrollment: false
    macos_enable_bootstrap_package_during_migration: false
    enable_end_user_authentication: true
    apple_enable_release_device_manually: true
    apple_setup_assistant: ../lib/dep-profile.json
    macos_script: ../lib/macos-setup-script.sh
    enable_managed_local_account: true
  macos_migration:
    enable: true
    mode: voluntary
    webhook_url: https://example.org/webhook_handler
  apple_account_provisioning:
    oauth_idp_token_url: https://automdm-example.okta.com/oauth2/v1/token
    oauth_idp_client_id: Ooa12345abcdeFGHI678
    oauth_idp_client_secret: a1b2c3d4e5
```

### macos_updates

_Available in AutoMDM Premium._

- `minimum_version` specifies the minimum required macOS version. Accepts a specific version number (e.g., `"15.4.1"`) or `"latest"` (latest macOS version available for the host's hardware). Must be paired with `deadline` when set to a version number, or `deadline_days` when set to an automatic option. (default: `""`).
- `deadline` specifies the deadline in `YYYY-MM-DD` format. The exact deadline is set to noon local time. Required when `minimum_version` is a specific version number. Cannot be used when `minimum_version` is set to `"latest". (default: `""`).
- `deadline_days` specifies the number of days after Apple releases an update before hosts are required to install it. Required when `minimum_version` is set to "latest". Cannot be used when `minimum_version` is a specific version number. (default: `null`).
- `update_new_hosts` - macOS hosts that automatically enroll (ADE) are updated to [Apple's latest version](https://automdm.com/guides/enforce-os-updates) during macOS Setup Assistant. For backwards compatibility, if not specified, and `deadline` and `minimum_version` are set, `update_new_hosts` is set to `true`. Otherwise, `update_new_hosts` defaults to `false`.

### ios_updates

_Available in AutoMDM Premium._

- `minimum_version` specifies the minimum required iOS version. Accepts a specific version number (e.g., `"15.4.1"`) or `"latest"` (latest iOS version available for the host's hardware). Must be paired with `deadline` when set to a version number, or `deadline_days` when set to an automatic option. (default: `""`).
- `deadline` specifies the deadline in `YYYY-MM-DD` format. The exact deadline is set to noon local time. Required when `minimum_version` is a specific version number. Cannot be used when `minimum_version` is set to `"latest"`. (default: `""`).
- `deadline_days` specifies the number of days after Apple releases an update before hosts are required to install it. Required when `minimum_version` is set to "latest". Cannot be used when `minimum_version` is a specific version number. (default: `null`).

### ipados_updates

_Available in AutoMDM Premium._

- `minimum_version` specifies the minimum required iPadOS version. Accepts a specific version number (e.g., `"15.4.1"`) `"latest"` (latest iPadOS version available for the host's hardware). Must be paired with `deadline` when set to a version number, or `deadline_days` when set to an automatic option. (default: `""`).
- `deadline` specifies the deadline in `YYYY-MM-DD` format. The exact deadline is set to noon local time. Required when `minimum_version` is a specific version number. Cannot be used when `minimum_version` is set to `"latest"`. (default: `""`).
- `deadline_days` specifies the number of days after Apple releases an update before hosts are required to install it. Required when `minimum_version` is set to "latest". Cannot be used when `minimum_version` is a specific version number. (default: `null`).

### windows_updates

_Available in AutoMDM Premium._

- `deadline_days` specifies the number of days before Windows installs updates (default: `null`)
- `grace_period_days` specifies the number of days before Windows restarts to install updates (default: `null`)

### apple_settings and windows_settings

- `enable_disk_encryption` specifies whether to enforce disk encryption on hosts (default: `false`). _Available in AutoMDM Premium._
- `enable_escrow_disk_encryption_key` specifies whether AutoMDM stores the disk encryption recovery key without prompting users to turn on disk encryption. Set to `true` when a third-party tool handles enforcement. Only supported on macOS (`apple_settings`). _Available in AutoMDM Premium._
- `require_bitlocker_pin` specifies whether end users on Windows hosts must set a BitLocker PIN, which is required to unlock the host during startup. `enable_disk_encryption` must be `true`. Only supported on Windows (`windows_settings`). _Available in AutoMDM Premium._
- `end_user_local_account_type` specifies the end user account type for macOS hosts. Requires `setup_experience.enable_managed_local_account` to be `true`. Only supported on macOS (`apple_settings`). Default: `"admin"`. To force a standard user account on Windows, use the [Autopilot profile](https://automdm.com/guides/windows-mdm-setup#force-a-standard-user-account).
- `enable_managed_local_account` specifies whether to create the managed local account on that platform (default: `false`). Currently Windows only. macOS is [coming soon](https://github.com/fleetdm/fleet/issues/50084).
- `configuration_profiles` is a list of configuration profiles. Accepts .mobileconfig/.json (macOS/iOS/iPadOS) or .xml (Windows).
- `name` specifies the display name for the profile. If not specified, the name is derived from the profile file.
- `description` specifies an optional description for the profile.

### linux_settings
- `enable_escrow_disk_encryption_key` specifies whether AutoMDM escrows the disk encryption key for Linux hosts with an encrypted disk (default: false). When set to true, AutoMDM Desktop prompts the user to enter their current encryption passphrase, generates a new passphrase, adds it as a LUKS keyslot, and securely stores it in AutoMDM. _Available in AutoMDM Premium._

> `activation` is a path to a JSON file containing custom activation criteria for declaration (DDM) profiles. Only supported for Apple declaration profiles (.json with DDM format). If not specified, no custom activation is applied.

> PayloadScope set to "User" in a DDM declaration's top-level JSON is required for user-scoped payloads, see [Custom OS settings](https://automdm.com/guides/custom-os-settings#macos) for details.`

Use `self_service` to specify whether end users can manually install from **AutoMDM Desktop > Controls**. When set to true, profile will not be deployed automatically and is opt-in. Only supported for `.mobileconfig` profiles. Labels still decide which hosts can see the profile.

Use `hidden` to specify whether to hide the profile from the end user by default on **AutoMDM Desktop > Controls**. End users can toggle "Show hidden profiles" in the UI to view all profiles on the host, but these profiles do not require the end user to take any action. `self_service` must be set to `false` (force install of profile) to use this option. Supported for `.mobileconfig`, declaration (`.json`), and Windows (`.xml`) profiles.

### android_settings

- `android_settings.configuration_profiles` is a list of Android configuration profiles (.json).
- `name` specifies the display name for the profile. If not specified, the name is derived from the profile file.
- `description` specifies an optional description for the profile.

Each entry can use either `path:` or `paths:`. Filenames must not contain `*`, `?`, `[`, or `{` when using `path:`. See [`path:` vs `paths:`](#path-vs-paths-glob-patterns) for glob pattern support.

Use `labels_include_all` to target hosts that have all labels, `labels_include_any` to target hosts that have any label, or `labels_exclude_any` to target hosts that don't have any of the labels. Only one of `labels_include_all` or `labels_include_any` can be specified. `labels_exclude_any` can be used on its own or combined with either one to exclude hosts from the included set. A label can't appear in both an include and an exclude list. If none are specified, all hosts are targeted.

Use `hidden` to mark a profile that doesn't require any action from the end user. Hidden profiles show a hidden indicator on **Host details > Controls**. `self_service` isn't supported for Android profiles.

#### android_settings.certificates

- `name` is the name of the certificate. Name can be used as a certificate alias to reference in configuration profiles (custom settings).
- `certificate_authority_name` is the name of the [certificate authority (CA)](#certificate-authorities) to issue the certificate from. Currently, only a custom SCEP CA is supported.
- `subject_name` is the certificate's subject name (SN). Separate subject fields with a comma (`,`). For example: "/CN=john@example.com/O=Acme Inc.".
- `subject_alternative_name` is the certificate's subject alternative name (SAN). Separate SAN fields with a comma (`,`). Each field is a key-value pair. Supported keys (case-insensitive) are:
  - `DNS` for a DNS hostname (e.g. `DNS=wifi.example.com`).
  - `EMAIL` for an email address / RFC 822 name (e.g. `EMAIL=john@example.com`).
  - `UPN` for a Microsoft User Principal Name (e.g. `UPN=john@corp.example.com`), commonly used for Active Directory / Intune Wi-Fi authentication.
  - `IP` for an IPv4 or IPv6 address (e.g. `IP=10.0.0.1` or `IP=2001:db8::1`).
  - `URI` for a URI (e.g. `URI=spiffe://example.com/workload/wifi`).

  Example: `"DNS=wifi.example.com, UPN=$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME"`.

You can use [AutoMDM's host variables](https://automdm.com/guides/automdm-variables) in `subject_name` and `subject_alternative_name` to make the certificate unique to each host.

### apple_account_provisioning

_Available in AutoMDM Premium._

The `apple_account_provisioning` section can only be configured for "All teams" (`default.yml`) and only supports macOS hosts today

  - `oauth_idp_token_url` is the token URL for your Oauth ROPG(Resource Owner Password Grant) IdP. For Okta it is normally of the form https://your-okta-domain.okta.com/oauth2/v1/token
  - `oauth_idp_client_id` is the client ID of your Oauth ROPG application within your IdP. In Okta this can be found under your application's Client Credentials
  - `oauth_idp_client_secret` is the client secret of your Oauth ROPG application within your IdP

### setup_experience

_Available in AutoMDM Premium._

The `setup_experience` section lets you control the out-of-the-box [setup experience](https://automdm.com/guides/setup-experience).

- `macos_bootstrap_package` is the URL to a bootstrap package. AutoMDM will download the bootstrap package. Applies to macOS only (default: `""`).
- `macos_enable_bootstrap_package_during_manual_enrollment` specifies whether the bootstrap package is also installed on macOS hosts that turn on MDM manually, rather than automatically via Apple Business. This includes re-enrollment. Requires `bootstrap_package`. Applies to macOS only (default: `false`).
- `macos_enable_bootstrap_package_during_migration` specifies whether the bootstrap package is installed on macOS hosts assigned to AutoMDM in Apple Business that migrate from another MDM, including re-enrollment via `sudo profiles renew`. Requires `bootstrap_package`. Applies to macOS only (default: `false`).
- `macos_manual_agent_install` specifies whether AutoMDM's agent (automdmd) will be installed as part of setup experience. When `true`, automdmd also isn't installed on hosts that turn on MDM manually if the bootstrap package is installed during manual enrollment. Applies to macOS only (default: `false`)
- `enable_end_user_authentication` specifies whether or not to require IdP authentication when the user first sets up their host. Applies to macOS, Windows, Linux, iOS/iPadOS, and Android.
- `require_all_software_macos` specifies whether to cancel setup on a macOS host if any software installs fail.
- `require_all_software_windows` specifies whether to cancel setup on a Windows host if any software installs fail. When `true`, the host is blocked at the Windows Enrollment Status Page and the end user must reset the device to try again. When `false`, the Enrollment Status Page lists the software that failed and the end user can continue to the desktop and install it later via self-service.
- `lock_end_user_info` specifies whether or not to enable end user to edit the local account Account Name and Full Name in macOS Setup Assistant. (default: `true`)
- `require_all_software` specifies whether to cancel setup on a macOS host if any software installs fail.
- `apple_enable_release_device_manually` when enabled, you're responsible for sending the [`DeviceConfigured` command](https://developer.apple.com/documentation/devicemanagement/device-configured-command). End users will be stuck in Setup Assistant until this command is sent. Applies to Apple (macOS, iOS, iPadOS) hosts that automatically enroll via Apple Business (AB).
- `apple_setup_assistant` is a path to a custom [automatic enrollment (ADE) profile](https://support.apple.com/guide/deployment/automated-device-enrollment-management-dep73069dd57/web) (.json). Applies to macOS and iOS/iPadOS hosts.
- `macos_script` is the path to a custom setup script to run after the host is first set up. Applies to macOS only.

`enable_managed_local_account` and `end_user_local_account_type` at this level are deprecated. 
Please use the platform-specific `apple_settings.managed_local_account_settings`, `apple_settings.end_user_local_account_type`, or `windows_settings.enable_managed_local_account` instead.

#### Example

`teams/team-name.yml`, or `teams/unassigned.yml`

```yaml
setup_experience:
  macos_bootstrap_package: "https://your-storage/package.pkg"
  macos_enable_bootstrap_package_during_manual_enrollment: false
  macos_enable_bootstrap_package_during_migration: false
  macos_manual_agent_install: false
  enable_end_user_authentication: true
  lock_end_user_info: true
  apple_enable_release_device_manually: false
  apple_setup_assistant: "./setup_assistant.json"
  macos_script: "./post_setup.sh"
```

### macos_migration

_Available in AutoMDM Premium._

The `macos_migration` section lets you control the [end user migration workflow](https://automdm.com/docs/using-automdm/mdm-migration-guide#end-user-workflow) for macOS hosts that enrolled to your old MDM solution.

- `enable` specifies whether or not to enable end user migration workflow (default: `false`)
- `mode` specifies whether the end user initiates migration (`voluntary`) or they're nudged every 15-20 minutes to migrate (`forced`) (default: `""`).
- `webhook_url` is the URL that AutoMDM sends a webhook to when the end user selects **Start**. Receive this webhook using your automation tool (ex. Tines) to unenroll your end users from your old MDM solution.

Can only be configured for "All teams" (`default.yml`).

### Referencing and targeting configuration profiles

Each entry can use either `path:` or `paths:`:

- **`path:`** references a single file. Filenames must not contain `*`, `?`, `[`, or `{`.
- **`paths:`** accepts a [glob pattern](#path-vs-paths-glob-patterns) to match multiple files (e.g. `../lib/windows/profiles/*.xml`). Labels and other options specified on a `paths:` entry apply to all matched files.

Use `labels_include_all` to target hosts that have all labels, `labels_include_any` to target hosts that have any label, or `labels_exclude_any` to target hosts that don't have any of the labels. Only one of `labels_include_all` or `labels_include_any` can be specified. `labels_exclude_any` can be used on its own or combined with either one to exclude hosts from the included set. A label can't appear in both an include and an exclude list. If none are specified, all hosts are targeted.

## software

The `software` section allows you to configure packages, store apps (Apple App Store and Google Play Store), and AutoMDM-maintained apps that you want to install on your hosts.

- `packages` is a list of paths to custom packages (.pkg, .ipa, .msi, .exe, .deb, .rpm, .tar.gz, .sh, .py, or .ps1).
- `app_store_apps` is a list of Apple App Store or Google Play Store apps.
- `automdm_maintained_apps` is a list of AutoMDM-maintained apps.
- `patch_deadline_settings` sets when AutoMDM patches apps that have `patch_deadline` enabled. See [patch_deadline_settings](#patch-deadline-settings).

Currently, you can specify `install_software` in the [`policies` YAML](#policies) to automatically install software when a host fails a policy.

A software title can have more than one package (see [packages](#packages)) or more than one App Store app version (see [app_store_apps](#app_store_apps)) on the same team. It can't mix types: if you specify Google Chrome for macOS once in `packages` and once in `automdm_maintained_apps`, only one of them will be added to AutoMDM.

The same custom package at different versions can upload side by side.
This means, if you specify a Google Chrome at the same version for macOS twice in `packages` or once in `packages` and once in `automdm_maintained_apps`, only one of them will be added to AutoMDM. Google Chrome at two different versions (i.e. Google Chrome v1.0 and v2.0) will upload together and both will be available in AutoMDM.

Currently, when a `.ipa` file is added in `packages`, AutoMDM adds software for both iOS and iPadOS, along with all specified settings (e.g. `self_service`). If software for one platform is deleted in the UI, it will come back when GitOps is re-run.

Script-only packages (.sh, .ps1, .py) also support $AUTOMDM_SECRET_* variables. AutoMDM replaces them with their values when the install script is sent to the host.

#### Example

`teams/team-name.yml`, or `teams/unassigned.yml`

```yaml
software:
  patch_deadline_settings:
    days:
      - monday
      - wednesday
      - friday
    time: "11:00"
  packages:
    - path: ../lib/software-name.package.yml
      categories:
        - "🌎 Browsers"
      self_service: true
      setup_experience: true
    - path: ../lib/onboarding-script.sh.package.yml
      setup_experience_platform: darwin, linux
    - path: ../lib/software-name2.package.yml
  app_store_apps:
    - app_store_id: "6443476492"
      platform: ios
      setup_experience: true
      versions:
        - name: Production
          labels_exclude_any:
            - IT team
          self_service: true
          categories:
            - "🔐 Security"
          auto_update_enabled: true
          auto_update_window_start: "00:00"
          auto_update_window_end: "04:00"
          configuration:
            path: ../lib/software/ios-cloudflare-one-agent-config-production.xml
        - name: Test
          labels_include_any:
            - IT team
          configuration:
            path: ../lib/software/ios-cloudflare-one-agent-config-test.xml
    - app_store_id: "com.cloudflare.cloudflareoneagent"
      platform: android
      setup_experience: true
      versions:
        - name: Production
          self_service: true
          labels_exclude_any:
            - IT team
          configuration:
            path: ../lib/software/android-cloudflare-one-agent-config-production.json
        - name: Test
          self_service: true
          labels_include_any:
            - IT team
          configuration:
            path: ../lib/software/android-cloudflare-one-agent-config-test.json
  automdm_maintained_apps:
    - slug: slack/darwin
      version: "4.47.65"
      install_script:
        path: ../lib/software/slack-install-script.sh
      uninstall_script:
        path: ../lib/software/slack-uninstall-script.sh
      post_install_script:
        path: ../lib/software/slack-config-script.sh
      self_service: true
      setup_experience: true
      labels_include_any:
        - Design
        - Sales
      categories:
        - "👬 Communication"
        - "💻 Productivity"
    - slug: parallels/darwin
      version: "^26"
      self_service: true
      labels_include_any:
        - Engineering
```

#### self_service, labels, categories, setup_experience, and display_name

- `self_service` specifies whether end users can install from **AutoMDM Desktop > Self-service** (default: `false`) on macOS or [self-service web app](https://automdm.com/learn-more-about/deploy-self-service-to-ios) on iOS/iPadOS.
- `labels_include_all` targets hosts that **have all** of the specified labels. `labels_include_any` targets hosts that **have any** of the specified labels. `labels_exclude_any` targets hosts that **have none** of the specified labels. Only one of these fields can be set. If none are set, all hosts are targeted.
- `categories` is a list of self-service category names. Categories group self-service software on your end users' **AutoMDM Desktop > My device** page so that end users can filter by category and install all software in a category at once. They can be created and modified via GitOps.
  - Category names support emojis and can be up to 255 characters long. The uniqueness checks ignore emojis, so `"🌎 Browsers"` and `"🔍 Browsers"` are treated as the same name.
  - For AutoMDM-maintained apps, if `categories` is omitted, apps get their [default categories](https://github.com/anilyagiz/automdm/tree/main/ee/maintained-apps/outputs). If `categories` is empty, default categories are removed. If custom categories are specified, apps don't get their default categories unless they're specified explicitly. 
- `setup_experience` installs the software when hosts enroll (default: `false`). On Windows and Linux hosts, if the software has associated policies, AutoMDM checks them first and skips the install when the host passes all of them. Learn more in the [setup experience guide](https://automdm.com/guides/setup-experience).
- `setup_experience_platform` specifies which platform to target for the `.sh` and `.py` script-only packages and `.ipa` packages in setup experience. For `.sh` packages, options are `darwin` and `linux`. For `.ipa` packages, options are `ios` and `ipados`.For `.py` packages, options are `darwin`, `linux`, and `windows`. If not specified and `setup_experience` is `true`, Linux is the default platform for `.sh` and `ios` is the default for `.ipa`.
- `display_name` is a custom name that will be displayed in the UI. If not set, the default depends on the software type:
  - `packages`: the name [extracted from the package](https://automdm.com/guides/deploy-software-packages#package-metadata-extraction) is used. For script-only packages, the filename is used.
  - `automdm_maintained_apps`: the AutoMDM-maintained app name is used.
  - `app_store_apps`: the App Store or Google Play app name is used.

In all cases, once AutoMDM collects software inventory, the inventory name is used instead.

### patch_deadline_settings

_Available in AutoMDM Premium_

`patch_deadline_settings` sets when AutoMDM patches AutoMDM-maintained apps that have a [patch policy](#patch-policy) with `patch_deadline` set to `true`. AutoMDM shows the end user a notification 1 hour before the deadline.

- `days` is a list of days of the week when the deadline applies. Options are `monday`, `tuesday`, `wednesday`, `thursday`, `friday`, `saturday`, and `sunday`. Required.
- `time` is the deadline, formatted as `HH:MM` in the host's local time (e.g. `"11:00"`). AutoMDM doesn't patch a host until it has collected the host's timezone. Must be wrapped in quotes so it is processed as a string. Required.

If `patch_deadline_settings` is omitted, AutoMDM uses the default: Tuesday at `"17:00"`.

### packages

- `url` specifies the URL at which the software is located. AutoMDM will download the software and upload it to S3 (up to 3 attempts). If you don't want to host the package, add it to AutoMDM first and then copy the `hash_sha256`.
- `hash_sha256` specifies the SHA256 hash of the package file. If provided, and a package with that hash was already added to AutoMDM, the download will be skipped. This speeds up GitOps runs. If a package with that hash doesn't exist in AutoMDM, AutoMDM will download the package from the `url` and add the package if the hash matches. AutoMDM will error if the hash doesn't match. You can specify `hash_sha256` without `url` if the package was already added to AutoMDM via the UI or the API.
- `always_download` disables conditional HTTP downloads using ETag headers. By default (`false`), AutoMDM stores the ETag from the download response and sends it as `If-None-Match` on subsequent GitOps runs. If the server returns 304 Not Modified, the download is skipped entirely. Set to `true` to force AutoMDM to re-download the package on every GitOps run. Cannot be used together with `hash_sha256` (hash-pinned packages are already cached by hash). Not all servers support ETags correctly; if your download URL returns unreliable ETags, set `always_download: true`.
- `pre_install_query.path` is the SQL query AutoMDM runs before installing the software. Software will be installed only if the [query returns results](https://automdm.com/tables).
- `install_script.path` specifies the command AutoMDM will run on hosts to install software. The [default script](https://github.com/anilyagiz/automdm/tree/main/pkg/file/scripts) is dependent on the software type (i.e. .pkg). Not supported for `.sh`, `.py`, and `.ps1` files.
- `uninstall_script.path` is the script AutoMDM will run on hosts to uninstall software. The [default script](https://github.com/anilyagiz/automdm/tree/main/pkg/file/scripts) is dependent on the software type (i.e. .pkg).
- `post_install_script.path` is the script AutoMDM will run on hosts after the software install. There is no default.
- `icon.path` is a relative path to the PNG icon that will be displayed in AutoMDM and on **AutoMDM Desktop > Self-service** instead of the default icon built into AutoMDM. It must be a square PNG with dimensions between 120x120 px and 1024x1024 px, and 100 KB or smaller. Custom icons will only override the icon for the software title and team where they are added.

#### Example

##### Multiple versions of the same software

You can add multiple packages for the same software in a package YAML file. This enables staged rollouts and support of architecture-specific installers.

`self_service`, `categories`, and labels are defined per version. `setup_experience` is defined on the team-level.

If multiple versions target the same host, AutoMDM will deploy the one that was added first.

> In GitOps, the first package added is the first one in the package YAML file's list on the initial run that adds the title's packages. Reordering the list on a later run doesn't change the order.
>
> You can preview the order of the packages in the UI. The first package in the list is always a fallback in case multiple packages are scoped to the same host.

`teams/team-name.yml`, or `teams/unassigned.yml`

```yaml
software:
  packages:
    - path: ../lib/software/santa.package.yml
```

`lib/software/santa.package.yml`

```yaml
- url: https://github.com/northpolesec/santa/releases/download/2026.2/santa-2026.2.pkg
  install_script:
    path: santa-install-script.sh
  self_service: true
  labels_exclude_any:
    - IT test team
- url: https://github.com/northpolesec/santa/releases/download/2026.4/santa-2026.4.pkg
  install_script:
    path: santa-install-script.sh
  self_service: true
  categories:
    - "💻 Productivity"
  labels_include_all:
    - IT test team
```

##### URL

`lib/software-name.package.yml`:

```yaml
- url: https://dl.tailscale.com/stable/tailscale-setup-1.72.0.exe
  install_script:
    path: ../lib/software/tailscale-install-script.ps1
  uninstall_script:
    path: ../lib/software/tailscale-uninstall-script.ps1
  post_install_script:
    path: ../lib/software/tailscale-config-script.ps1
```

##### Hash

You can view the hash for existing software in the software detail page in the AutoMDM UI. It is also returned after uploading a new software item via the API.

```yaml
# Mozilla Firefox (Firefox 136.0.1.pkg) version 136.0.1
- hash_sha256: fd22528a87f3cfdb81aca981953aa5c8d7084581b9209bb69abf69c09a0afaaf
```

##### Conditional downloads

By default, AutoMDM uses conditional HTTP downloads to avoid re-downloading unchanged packages. On the first GitOps run, AutoMDM downloads the package normally and stores the server's ETag. On subsequent runs, AutoMDM sends a conditional GET request. If the server confirms the content hasn't changed (304 Not Modified), the download is skipped.

If your server doesn't support ETags reliably, you can disable this behavior with `always_download: true`:

```yaml
- url: https://dl.tailscale.com/stable/tailscale-setup-1.72.0.exe
  always_download: true
```

> Note: Conditional download is currently unsupported for .ipa files.

##### Script-only

Script-only packages (`.sh`, `.py`, and `.ps1` files) are referenced directly inline in the team's YAML file. The file contents become the install script. Script packages do not support `install_script` or automatic install (`install_software` in policies).

`self_service`, `categories`, `labels`, `icon`, `pre_install_query`, `post_install_script`, and `uninstall_script` are specified inline in the team's YAML file. There's no default uninstall script for script packages.

```yaml
software:
  packages:
    - path: ../lib/linux/scripts/vpn-setup.sh
      display_name: VPN setup
      icon:
         path: ../lib/icons/vpn-setup.png
      self_service: true
      categories:
        - "🛟 Support"
      labels_include_any:
      - Engineering
      - Customer Support
      pre_install_query:
        path: ../lib/linux/queries/vpn-not-installed.yml
      post_install_script:
        path: ../lib/linux/scripts/vpn-post-install.sh
      uninstall_script:
        path: ../lib/linux/scripts/vpn-uninstall.sh
```

### app_store_apps

App Store apps support `path:` (single file) and `paths:` (glob pattern) references, so you can define an app once in a separate file and reference it from multiple teams. See [`path:` vs `paths:`](#path-vs-paths-glob-patterns) for details. Filenames must not contain `*`, `?`, `[`, or `{` when using `path:`.

- `app_store_id` is the ID of the Apple App Store or Google Play Store app. You can find this ID at the end of the app's URL. For example, "Bear - Markdown Notes" URL is [https://apps.apple.com/us/app/bear-markdown-notes/id1016366447](https://apps.apple.com/us/app/bear-markdown-notes/id1016366447) making the `app_store_id` is "1016366447". Similarly, the URL for "Google Chrome" on Android is [https://play.google.com/store/apps/details?id=com.android.chrome](https://play.google.com/store/apps/details?id=com.android.chrome), so the `app_store_id` is "com.android.chrome."
  + For Apple App Store apps, make sure to include only the ID itself, and not the `id` prefix shown in the URL. The ID must be wrapped in quotes as shown in the example so that it is processed as a string.
- `platform` is the platform of the app (`darwin`, `ios`, `ipados`, or `android`). If not specified, and `app_store_id` is Apple App Store ID, one app for each of the Apple App Store app's supported platforms is added. For example, adding [Bear](https://apps.apple.com/us/app/bear-markdown-notes/id1016366447) (supported on iOS and iPadOS) adds both the iOS and iPadOS apps to your software that's available to install in AutoMDM.

- `icon.path` is a relative path to the PNG icon that will be displayed in AutoMDM and on **AutoMDM Desktop > Self-service** instead of the default icon the icon sourced from Apple. It must be a square PNG with dimensions between 120x120 px and 1024x1024 px, and 100 KB or smaller.. Custom icons will only override the icon for the software title and team where they are added.
- `display_name` is the name displayed in the AutoMDM UI. It applies to every version of the app. If omitted, AutoMDM uses the app's name from the App Store or Google Play.
- `setup_experience` installs the app when hosts enroll (default: `false`). It's defined for the app, not per version. Currently, custom targets (labels) don't apply during setup experience, so first added version will be always installed.
- `versions` is a list of one or more versions (instances) of the app. AutoMDM always installs the latest version available in the App Store or Google Play. Each version carries its own settings, which lets you give different groups of hosts a different configuration (e.g. a different VPN configuration per IdP group) without creating a separate team.
  - `name` identifies the version in AutoMDM (e.g. `Production`). It must be unique for the app on the team.
  - Each version defines its own `self_service`, `categories`, labels (`labels_include_any`, `labels_include_all`, `labels_exclude_any`), `auto_update_enabled`, `auto_update_window_start`, `auto_update_window_end`, and `configuration`. See [self_service, labels, categories, setup_experience, and display_name](#self-service-labels-categories-setup-experience-and-display-name) for the shared fields.
  - Up to 10 versions are supported per app.
  - If a host is in scope for more than one version, AutoMDM installs the one that was added first. Best practice is to scope every version if you add more than one, to prevent conflicts. A version with no labels is in scope on all hosts in the team, so if it's added first it's always the one applied.
- `configuration.path` is a relative path to the version's managed app configuration. For iOS and iPadOS apps it is in XML format, and for Google Play Store apps it is in JSON format. Currently only supported for iOS, iPadOS, and Android.
  - You can add an empty configuration if you want the same app installed on different hosts with and without a configuration. In that case the file referenced by `path` should only have `<dict></dict>` for iOS and iPadOS, or `{managedConfiguration: {}}` for Android.
  + Android: all fields from [Android application policy](https://developers.google.com/android/management/reference/rest/v1/enterprises.policies#ApplicationPolicy) are supported except `packageName` and `installType`.
  - Configuration keys vary by app. Refer to the app vendor's documentation for available managed configuration options. For example, see [Zoom's Android managed configuration](https://support.zoom.com/hc/en/article?id=zm_kb&sysparm_article=KB0064790), [Zoom's iOS managed configuration](https://support.zoom.com/hc/en/article?id=zm_kb&sysparm_article=KB0064102), or [GlobalProtect's Android configuration](https://docs.paloaltonetworks.com/globalprotect/10-1/globalprotect-admin/mobile-endpoint-management/manage-the-globalprotect-app-using-other-third-party-mdms/configure-the-globalprotect-app-for-android).
- `auto_update_enabled` enables automatic updates for the app (default: `false`). Only supported for iOS and iPadOS App Store (VPP) apps.
- `auto_update_window_start` is the start of the daily maintenance window during which AutoMDM will apply automatic updates, formatted as `HH:MM` in the host's local time (e.g. `"00:00"`). Required when `auto_update_enabled` is `true`. Must be wrapped in quotes so it is processed as a string.
- `auto_update_window_end` is the end of the daily maintenance window, formatted as `HH:MM` in the host's local time (e.g. `"04:00"`). Required when `auto_update_enabled` is `true`. If the end time is earlier than the start time, the window wraps to the next day (e.g. `"22:00"` to `"02:00"`). Must be wrapped in quotes so it is processed as a string.
- `location` is the name of the organization unit in Apple Business for the VPP token that the Apple App Store app is added from (optional). Only needed if the team has more than one VPP token and you want to add the app from a VPP token other than the team's first-added VPP token. The VPP token must be assigned to the team in [`volume_purchasing_program`](#volume-purchasing-program). Not supported for Android apps.

To add the same App Store app for multiple platforms, specify the `app_store_id` multiple times, along with the `platform` you want. If you don't specify a platform, one app for each available platform will be added (macOS, iOS, and iPadOS).

#### Multiple VPP tokens

If a team has more than one VPP token, AutoMDM uses the first-added VPP token for Apple App Store apps without a `location`. The first-added VPP token is the one that was uploaded to AutoMDM first, out of the VPP tokens assigned to the team (including a VPP token assigned to "All teams"). If the app isn't available in that VPP token, GitOps fails. Specify `location` to add it from a different VPP token.

Each Apple App Store app can be added to a team from only one VPP token per platform. AutoMDM uses that VPP token to assign licenses.

If an app is already added to the team and `location` isn't specified, AutoMDM keeps the app's current VPP token. This way, adding a new VPP token to a team, or removing one, doesn't change the VPP token of apps that are already added. If you specify a `location` that's different from the app's current VPP token, GitOps fails. To move an app to a different VPP token, remove the app from your YAML, run GitOps, and then add the app back with the new `location`.

To keep the first-added VPP token, renew it instead of deleting it and uploading a new one. A VPP token that's deleted and uploaded again counts as the most recently added VPP token.

```yaml
software:
  app_store_apps:
    - app_store_id: "546505307" # Added from the automdm's first-added VPP token
      platform: ios
    - app_store_id: "1091189122" # Added from the second VPP token
      platform: ios
      location: Acme Japan
```

When you update an Android app's configuration via GitOps, the app's settings are applied without reinstalling the app. The install status will show as "Pending" until the configuration is applied.

With the default server configuration, GitOps supports over 200 Google Play Store apps per team. If `AUTOMDM_LIVE_QUERY_REST_PERIOD` is set to `30s` or less, GitOps can time out with fewer apps.

#### Example

##### Multiple versions of the same app

You can add more than one version of the same App Store app to a team. Each version has its own name, settings, and managed app configuration.

If a host is in scope for more than one version, AutoMDM will install the one that was added first.

> In GitOps, the first version added is the first one in the app's `versions` list on the initial run that adds the app. Reordering the list on a later run doesn't change the order.
>
> You can preview the order of the versions in the UI. The first version in the list is always a fallback in case a host is in scope for more than one version.

Currently, labels aren't evaluated during the setup experience on iOS and iPadOS hosts. Best practice is to first add the version that applies to the majority of hosts, because the first added will be applied during the setup experience. For example, add your production VPN configuration first, and the test configuration second. Once the host enrolls and labels are evaluated, the second version will be applied to scoped hosts.

`teams/team-name.yml`, or `teams/unassigned.yml`

```yaml
software:
  app_store_apps:
    - path: ../lib/software/zoom.app-store-app.yml
```

`lib/software/zoom.app-store-app.yml`

```yaml
- app_store_id: "6443476492"
  platform: ios
  versions:
    - name: Production
      categories:
        - "👬 Communication"
      labels_exclude_any:
        - Test
      configuration:
        path: ios-cloudflare-one-agent-config-production.xml
    - name: Test
      categories:
        - "👬 Communication"
      labels_include_any:
        - Test
      configuration:
        path: ios-cloudflare-one-agent-config-test.xml
```

### automdm_maintained_apps

AutoMDM-maintained apps support `path:` (single file) and `paths:` (glob pattern) references, so you can define an app once in a separate file and reference it from multiple teams. See [`path:` vs `paths:`](#path-vs-paths-glob-patterns) for details. Filenames must not contain `*`, `?`, `[`, or `{` when using `path:`.

- `automdm_maintained_apps` is a list of AutoMDM-maintained apps. Provide the `slug` field to include an AutoMDM-maintained app on a team. To find the `slug`, head to **Software > Add software** and select an AutoMDM-maintained app, then select **Show details**. You can also see the [list of app slugs on GitHub](https://github.com/anilyagiz/automdm/blob/main/ee/maintained-apps/outputs/apps.json).

By default, AutoMDM-maintained apps will be updated to the latest version published by AutoMDM when GitOps runs.

The fields below are all optional.

- `self_service` specifies whether end users can install from **AutoMDM Desktop > Self-service**.
- `pre_install_query.path` is the SQL query AutoMDM runs before installing the software. Software will be installed only if the [query returns results](https://automdm.com/tables).  If a [patch policy](#patch-policy) has `patch_when_closed`, `notify_before_patching`, or `patch_deadline` set to `true`, AutoMDM manages this query and rejects this field.
- `post_install_script.path` is the script that, if supplied, AutoMDM will run on hosts after the software installs.
- `icon.path` is a relative path to the PNG icon that will be displayed in AutoMDM and on **AutoMDM Desktop > Self-service** instead of the default icon the icon sourced from Apple. It must be a square PNG with dimensions between 120x120 px and 1024x1024 px, and 100 KB or smaller. Custom icons will only override the icon for the software title and team where they are added.
- `⁠version` specifies the app version. Available versions are listed in the AutoMDM UI under **Actions > Versions**. If omitted, AutoMDM automatically downloads the latest version found in [AutoMDM's catalog](https://automdm.com/software-catalog). The `version` must be wrapped in quotes (e.g. "147.0.1") so that it is processed as a string.
  - To pin to the major version, use a caret (`^`) constraint. You can specify only the major version, without the minor and patch versions. For example, `"⁠^147"` means that AutoMDM will continuously download the latest version until the app updates to 148.0.
- `display_name` is the package name that will be displayed in the UI. If not set, `name` will be used instead.

If the fields below are omitted, they default to values specified in [the app's metadata on GitHub](https://github.com/anilyagiz/automdm/tree/main/ee/maintained-apps/outputs).

- `install_script.path` specifies the command AutoMDM will run on hosts to install software.
- `uninstall_script.path` is the script AutoMDM will run on hosts to uninstall software.
- `categories` is an array of categories, see [categories](#self-service-labels-categories-and-setup-experience).

#### Separate file

`teams/team-name.yml`, or `teams/unassigned.yml`

```yaml
software:
  automdm_maintained_apps:
    - path: ../lib/software/communication-apps.fma.yml
    - paths: "../lib/software/fma/*.yml"
```

A `path:`/`paths:` file can define more than one app, so related apps can be grouped together:

`lib/software/communication-apps.fma.yml`

```yaml
- slug: slack/darwin
  version: "4.47.65"
  self_service: true
  categories:
    - "👬 Communication"
- slug: zoom/darwin
  self_service: true
  categories:
    - "👬 Communication"
```

## org_settings and settings

Currently, managing users and ticket destinations (Jira and Zendesk) are only supported using AutoMDM's UI or [API](https://automdm.com/docs/rest-api/rest-api).

### features

The `features` section of the configuration YAML lets you turn on/off AutoMDM features.
- `additional_queries` adds extra host details. This information will be updated at the same time as other host details and is returned by the API when host objects are returned (default: empty).
- `enable_host_users` specifies whether or not AutoMDM collects user data from hosts (default: `true`).
- `enable_software_inventory` specifies whether or not AutoMDM collects software inventory from hosts (default: `true`).
- `historical_data` controls per-dataset collection of the data that drive the dashboard charts. Each sub-key defaults to `true`:
  - `uptime` — host activity samples that drive the **Hosts active** dashboard chart.
  - `vulnerabilities` — per-host software vulnerability data that drive the **Vulnerability exposure** dashboard chart. _Available in AutoMDM Premium._ AutoMDM Free doesn't collect this data, because the chart that reads it requires AutoMDM Premium.
- `vulnerability_exposure_historical_reporting` lets you define and persist the default filters for the **Vulnerability exposure** dashboard chart (risk registry) when the page loads. These filter display only and don't change which data AutoMDM collects. A user can still adjust the filters in the UI, but these changes aren't saved. `historical_data.vulnerabilities` must be enabled.
  - `software_filters` is the list of software categories to show. Valid values: `os` (operating system), `browsers` (Google Chrome, Safari, Mozilla Firefox, Brave, and Opera), `office` (Word, Excel, PowerPoint, and Outlook), and `adobe` (Acrobat, Flash, and Shockwave Player) (default: all categories).
  - `cvss_min` / `cvss_max` filters vulnerabilities by severity (CVSS version 3.x base score, range 0 to 10). Omitting a bound leaves that side unfiltered, which isn't the same as setting it to `0` or `10`: vulnerabilities that AutoMDM has no CVSS score for are shown only when neither bound is set.
  - `epss_min` / `epss_max` filters vulnerabilities by probability of exploit ([EPSS](https://www.first.org/epss/)) score (range 0 to 100).
  - `has_known_exploit`, when `true`, only includes software that has vulnerabilities which have been actively exploited in the wild ([CISA KEV](https://www.cisa.gov/known-exploited-vulnerabilities-catalog)) (default: `false`).
  - `exclude_vulnerabilities` is a list of specific CVEs to exclude.
 
A dataset is collected for a given host only when the sub-key is `true` at both the global level (`org_settings.features.historical_data`) and the host's team level (`settings.features.historical_data`). Setting a sub-key to `false` at either level disables collection for the affected hosts. Flipping the global sub-key off disables it for every team, regardless of per-team settings.

Can be configured for "All teams" (`org_settings`) and specific teams (`settings`).

#### Example

```yaml
org_settings:
  features:
    additional_queries:
      time: SELECT * FROM time
      macs: SELECT mac FROM interface_details
    enable_host_users: true
    enable_software_inventory: true
    historical_data:
      uptime: true
      vulnerabilities: false
    vulnerability_exposure_historical_reporting:
      software_filters:
        - os
        - browsers
        - office
        - adobe
      has_known_exploit: true
      cvss_min: 9
      cvss_max: 10
      epss_min: 0
      epss_max: 100
      exclude_vulnerabilities:
        - CVE-2025-50897
        - CVE-2025-76306
```

### automdm_desktop

The `automdm_desktop` section lets you customize the AutoMDM Desktop experience.
- `transparency_url` directs end users to a custom URL when they select **About AutoMDM** in the AutoMDM Desktop dropdown (default: [https://automdm.com/transparency](https://automdm.com/transparency)).
- `alternative_browser_host` is a custom hostname that my hosts will access AutoMDM Desktop from.
- `sso_enabled` determines whether to require IdP authentication in addition to the AutoMDM Desktop token to access the device page.

> `sso_enabled` doesn't enforce SSO while a host (macOS, Windows, or Linux) is completing [setup experience](https://automdm.com/guides/setup-experience). To require SSO during setup, use [setup_experience.enable_end_user_authentication](https://automdm.com/docs/configuration/yaml-files#setup-experience).

Can only be configured for "All teams" (`org_settings`).

#### Example

```yaml
org_settings:
  automdm_desktop:
    transparency_url: https://example.org/transparency
    alternative_browser_host: automdm-desktop.example.com
    sso_enabled: false
```

### gitops

The `gitops` section allows configuring [GitOps mode](https://automdm.com/learn-more-about/ui-gitops-mode) in the AutoMDM UI. When GitOps mode is enabled, many UI features become read-only to ensure that configuration changes are made only via GitOps.

- `gitops_mode_enabled` — when `true`, AutoMDM's UI shows GitOps-managed sections as read-only with a tooltip pointing to the repository URL. Requires AutoMDM Premium.
- `repository_url` (default: `""`) — the URL of the GitOps repository that manages this AutoMDM. Must be a valid `http://` or `https://` URL. Required when `gitops_mode_enabled: true`.

Can only be configured for "All teams" (`org_settings`).

> GitOps mode exceptions for labels, software, and enroll secrets can't be set in YAML. Configure them in the AutoMDM UI under **Settings** > **Integrations** > **Change management**. Learn more in the [GitOps mode guide](https://automdm.com/guides/gitops-mode).

> If `gitops:` is not provided in your YAML file, any existing GitOps mode settings will be preserved.

#### Example

```yaml
org_settings:
  gitops:
    gitops_mode_enabled: true
    repository_url: https://github.com/example/automdm-config
```

### host_expiry_settings

The `host_expiry_settings` section lets you define if and when hosts should be automatically deleted from AutoMDM if they have not checked in.
- `host_expiry_enabled` (default: `false`)
- `host_expiry_window` if a host has not communicated with AutoMDM in the specified number of days, it will be removed. Must be > `0` when host expiry is enabled (default: `0`).

If this setting is not defined in your YAML files, unlike all other settings, it will not get reset to the default values.

Can be configured for "All teams" (`org_settings`) and specific teams (`settings`).

`host_expiry_window` does nothing unless `host_expiry_enabled` is `true` in `org_settings` or on the team.

Setting `host_expiry_enabled: false` on a team tells that team to use the `org_settings` value. It doesn't exempt the team. To apply host expiry to only some teams, leave it off in `org_settings` and turn it on for those teams.

AutoMDM measures the window from the host's last check-in, not from **Last fetched**. Host expiry skips hosts assigned to AutoMDM in Apple Business or Windows Autopilot. If hosts aren't expiring as expected, see [Why aren't my hosts being deleted after the host expiry window?](https://automdm.com/docs/get-started/faq#why-arent-my-hosts-being-deleted-after-the-host-expiry-window) in the FAQ.

#### Example

```yaml
org_settings:
  host_expiry_settings:
    host_expiry_enabled: true
    host_expiry_window: 10
```

### activity_expiry_settings

The `activity_expiry_settings` section lets you define how to handle activities.
- `activity_expiry_enabled` when enabled, allows automatic cleanup of activities (and associated live query data) older than the specified number of days. Activities linked to a host are preserved until the host is deleted.
- `activity_expiry_window` the number of days to retain activity records, if activity expiry is enabled.
- `preserve_host_activities_on_reenrollment` when enabled, preserves host activities after a wipe and re-enrollment. Supported for company-owned (AB) Apple hosts and Android hosts. **Delete activities > Max activity age** still applies. (Default: `false` on new AutoMDM instances. AutoMDM sets it to `true` when you upgrade an existing instance, so that prior behavior is preserved.)

#### Example

```yaml
org_settings:
  activity_expiry_settings:
    activity_expiry_enabled: true
    activity_expiry_window: 30
    preserve_host_activities_on_reenrollment: true
```

### org_info

- `org_name` is the name of your organization (default: `""`)
- `org_logo_path_dark_mode` is a path to an image file for your organization's logo (default: AutoMDM logo). Only one of `org_logo_path_dark_mode` or `org_logo_url_dark_mode` may be specified. 
- `org_logo_path_light_mode` is a path to an image file for your organization's logo (default: AutoMDM logo). Only one of `org_logo_path_light_mode` or `org_logo_url_light_mode` may be specified.
- `org_logo_url_dark_mode` is a public URL of the logo for your organization (default: AutoMDM logo). Only one of `org_logo_path_dark_mode` or `org_logo_url_dark_mode` may be specified.
- `org_logo_url_light_mode` is a public URL of the logo for your organization that can be used with light backgrounds (default: AutoMDM logo). Only one of `org_logo_path_light_mode` or `org_logo_url_light_mode` may be specified.
- `contact_url` is a URL or [file URI](https://en.wikipedia.org/wiki/File_URI_scheme) that appears in error messages presented to end users (default: `"https://automdm.com/company/contact"`)

Can only be configured for "All teams" (`org_settings`).

To get the best results for your logos (`org_logo_url_dark_mode`/`org_logo_path_dark_mode` and `org_logo_url_light_mode`/`org_logo_path_light_mode`), use the following sizes:
- For square logos, use a PNG, JPEG/JPG, WebP or SVGs that's 256x256 pixels (px).
- For rectangular logos (wordmark), use a PNG, JPEG/JPG, WebP or SVGs that's 516x256 pixels (px).

#### Example

```yaml
org_settings:
  org_info:
    org_name: AutoMDM
    org_logo_url_dark_mode: https://example.com/logo.png
    org_logo_url_light_mode: https://example.com/logo-light.png
    contact_url: https://automdm.com/company/contact
```

### secrets

The `secrets` section defines the valid secrets that hosts can use to enroll to AutoMDM. Supply one of these secrets when generating the automdmd agent you'll use to [enroll hosts](https://automdm.com/docs/using-automdm/enroll-hosts).

Can be configured for "All teams" (`org_settings`) and specific teams (`settings`).

#### Example

```yaml
org_settings:
  secrets:
  - secret: $ENROLL_SECRET
```

### server_settings

- `ai_features_disabled` disables AI-assisted policy descriptions and resolutions. (default: `false`)
- `enable_analytics` specifies whether or not to enable AutoMDM's [usage statistics](https://automdm.com/docs/using-automdm/usage-statistics). (default: `true`)
- `live_reporting_disabled` disables the ability to run live reports (ad hoc reports executed via the UI or automdmctl). (default: `false`)
- `mcp_disabled` turns off AutoMDM's built-in [MCP server](https://automdm.com/guides/automdm-mcp), so AI tools can't connect to it. (default: `false`) _Available in AutoMDM Premium._
- `discard_reports_data` disables storing results for all reports and deletes existing stored data. If set to `true`, data is still sent to the configured log destination if `automations_enabled`. (default: `false`)
- `report_cap` sets the maximum number of results to store per report before the report is clipped. If the number of hosts is higher than this cap, AutoMDM uses the number of hosts instead, so a report that returns one result per host is never clipped. This cap doesn't change the per-host limit: AutoMDM doesn't store a host's results if the host returns more than 1,000 rows. If increasing this cap, we recommend enabling reports for one query at a time and monitoring your infrastructure. (default: `1000`)
- `scripts_disabled` blocks access to run scripts. Scripts may still be added in the UI and CLI. (default: `false`)
- `server_url` is the base URL of the AutoMDM instance. If this URL changes and Apple (macOS, iOS, iPadOS) hosts already have MDM turned on, the end users will have to turn MDM off and back on to use MDM features. (default: provided during AutoMDM setup)


Can only be configured for "All teams" (`org_settings`).

#### Example

```yaml
org_settings:
  server_settings:
    ai_features_disabled: false
    enable_analytics: true
    live_reporting_disabled: false
    mcp_disabled: false
    discard_reports_data: false
    scripts_disabled: false
    server_url: https://instance.automdm.com
```


### sso_settings

The `sso_settings` section lets you define [single sign-on (SSO)](https://automdm.com/docs/deploying/configuration#configuring-single-sign-on-sso) settings.

- `enable_sso` (default: `false`)
- `idp_name` is the human-friendly name for the identity provider that will provide single sign-on authentication (default: `""`).
- `idp_image_url` is an optional link to an image such as a logo for the identity provider. (default: `""`).
- `entity_id` is the entity ID: a Uniform Resource Identifier (URI) that you use to identify AutoMDM when configuring the identity provider. It must exactly match the Entity ID field used in identity provider configuration (default: `""`).
- `metadata` is the metadata (in XML format) provided by the identity provider. (default: `""`)
- `metadata_url` is the URL that references the identity provider metadata. Only one of  `metadata` or `metadata_url` is required (default: `""`).
- `enable_jit_provisioning` specifies whether or not to enable [just-in-time user provisioning](https://automdm.com/docs/deploy/single-sign-on-sso#just-in-time-jit-user-provisioning) (default: `false`). _Available in AutoMDM Premium._
- `enable_sso_idp_login` specifies whether or not to allow single sign-on login initiated by identity provider (default: `false`).
- `sso_server_url` is used if the URL your AutoMDM users (admins, maintainers, observers) use to login to AutoMDM via SSO is different than the base URL of your AutoMDM instance. If not configured, login via SSO will use the base URL of the AutoMDM instance.

Can only be configured for "All teams" (`org_settings`).

#### Example

```yaml
org_settings:
  sso_settings:
    enable_sso: true
    idp_name: Okta
    idp_image_url: https://www.okta.com/favicon.ico
    entity_id: https://example.okta.com
    metadata: $SSO_METADATA
    enable_jit_provisioning: true
    enable_sso_idp_login: true
    sso_server_url: https://admin.example.com # Optional, SSO will only work from this URL
```

### integrations

The `integrations` section lets you configure your Google Calendar, Conditional access (enabling/disabling for hosts in "Unassigned"), Jira, Zendesk, and the identity checks applied to the [Request certificate](https://automdm.com/docs/rest-api/rest-api#request-certificate) API. After configuration, you can enable [automations](https://automdm.com/docs/using-automdm/automations) like calendar event and ticket creation for failing policies. Currently, enabling ticket creation is only available using AutoMDM's UI or [API](https://automdm.com/docs/rest-api/rest-api) (YAML files coming soon).

This section also lets you connect Google Workspace to sync identity provider (IdP) host vitals directly from your directory.

Can be configured for "All teams" (`org_settings`) and specific teams (`settings`).

#### Example

`default.yml`

```yaml
org_settings:
  integrations:
    conditional_access_enabled: true
    google_calendar:
      - api_key_json: $GOOGLE_CALENDAR_API_KEY_JSON
        domain: automdm.com
    google_workspace:
      - api_key_json: $GOOGLE_WORKSPACE_API_KEY_JSON
        domain: automdm.com
        impersonated_user_email: admin@example.com
    google_conditional_access:
      customer_id: C01234abc
      admin_email: team-admin@example.com
      api_key_json: $GOOGLE_CONDITIONAL_ACCESS_API_KEY_JSON
    jira:
      - url: https://example.atlassian.net
        username: user1
        api_token: $JIRA_API_TOKEN
        project_key: PJ1
    zendesk:
      - url: https://example.zendesk.com
        email: user1@example.com
        api_token: $ZENDESK_API_TOKEN
        group_id: 1234
    certificates_idp_introspection_urls:
      - https://company.okta.com/oauth2/v1/introspect
    certificates_idp_client_ids:
      - 0oa1b2c3d4e5f6g7h8i9
    certificates_disable_host_end_user_binding: false
```

`/teams/team-name.yml`

At the team level, there is the additional option to enable conditional access, which blocks third party app sign-ins on hosts failing policies. (Available in AutoMDM Premium. Must have Okta or Microsoft Entra connected.)

`conditional_access_enabled` only applies to the [Okta](https://automdm.com/guides/okta-conditional-access-integration) and [Microsoft Entra](https://automdm.com/guides/entra-conditional-access-integration) integrations. Google conditional access (`google_conditional_access`) doesn't use it.

```yaml
integrations:
  conditional_access_enabled: true
```

#### google_calendar

For "All teams" (`org_settings`):

- `api_key_json` is the contents of the JSON file downloaded when you create your Google Workspace service account API key (default: `""`).
- `domain` is the primary domain used to identify your end user's work calendar (default: `""`).

For specific teams (`settings`):

- `enable_calendar_events` to enable calendar events for a team (default: `false`).
- `webhook_url` is the webhook URL triggered during a user's calendar event (default: `""`).

#### google_workspace

_Available in AutoMDM Premium._

Connecting Google Workspace populates identity provider (IdP) host vitals directly from your directory. While Google Workspace is configured, SCIM provisioning (Okta, Entra ID, etc.) is ignored; configure one or the other, not both.

- `api_key_json` is the contents of the JSON file downloaded when you create your Google Workspace service account API key. The service account must have domain-wide delegation enabled (default: `""`).
- `domain` is your Google Workspace primary domain (default: `""`).
- `impersonated_user_email` is a Google Workspace admin the service account impersonates via domain-wide delegation (default: `""`).

Can be configured for "All teams" (`org_settings`).

#### google_conditional_access

_Available in AutoMDM Premium._

Turning on Google conditional access marks company-owned iOS, iPadOS, and Android hosts as managed in Google, so Google Context-Aware Access can block sign-in from hosts that aren't managed by AutoMDM. Learn more in the [Conditional access: Google](https://automdm.com/guides/google-conditional-access-integration) guide.

- `customer_id` is your Google customer ID (for example, `C01234abc`). Find it in the Google Admin console under **Account > Account settings** (default: `""`).
- `admin_email` is the email of the Google Workspace admin AutoMDM uses to mark hosts as managed (default: `""`).
- `api_key_json` is the contents of the JSON file downloaded when you create your service account key. The service account must have domain-wide delegation for the `https://www.googleapis.com/auth/cloud-identity.devices` scope. If you leave it out, AutoMDM keeps the key that's already uploaded (default: `""`).

AutoMDM checks the key with Google when it changes, including in a dry run (`automdmctl gitops --dry-run`).

To turn off Google conditional access, remove `google_conditional_access`. This deletes the key, so you need to set `api_key_json` to turn it back on. It doesn't unmark hosts in Google. Learn how to [disable Google conditional access](https://automdm.com/guides/google-conditional-access-integration#disable).

Can be configured for "All teams" (`org_settings`).

#### jira

- `url` is the URL of your Jira (default: `""`)
- `username` is the username of your Jira account (default: `""`).
- `api_token` is the Jira API token (default: `""`). Atlassian API tokens [expire after one year at most](https://support.atlassian.com/atlassian-account/docs/manage-api-tokens-for-your-atlassian-account/), so you'll have to create a new token and update this integration every year.
- `project_key` is the project key location in your Jira project's URL. For example, in "jira.example.com/projects/EXMPL," "EXMPL" is the project key (default: `""`).

Can be configured for "All teams" (`org_settings`). Use API to configure Jira for specific teams or "Unassigned" hosts.

#### zendesk

- `url` is the URL of your Zendesk (default: `""`)
- `username` is the username of your Zendesk account (default: `""`).
- `api_token` is the Zendesk API token (default: `""`).
- `group_id`is found by selecting **Admin > People > Groups** in Zendesk. Find your group and select it. The group ID will appear in the search field.

Can be configured for "All teams" (`org_settings`). Use API to configure Zendesk for specific teams or "Unassigned" hosts.

#### certificates_idp_introspection_urls, certificates_idp_client_ids, and certificates_disable_host_end_user_binding

These settings control identity verification on the [Request certificate](https://automdm.com/docs/rest-api/rest-api#request-certificate) API. Can only be configured for "All teams" (`org_settings`).

- `certificates_idp_introspection_urls` (AutoMDM Premium): allowlist of OAuth 2.0 token introspection URLs accepted in `idp_oauth_url`. Entries must be absolute `https` URLs without embedded credentials and are matched exactly. While empty, requests that include IdP credentials are rejected. Once it has entries, `idp_oauth_url`, `idp_token`, and `idp_client_id` are required on every request (default: `[]`).
- `certificates_idp_client_ids` (AutoMDM Premium): optional allowlist of OAuth client IDs accepted in `idp_client_id`. Requires `certificates_idp_introspection_urls` to have entries (default: `[]`).
- `certificates_disable_host_end_user_binding`: by default, requests authenticated with an HTTP signature must carry a CSR whose email and UPN match the end user AutoMDM has recorded for the host, and hosts with no recorded end user are rejected. Set to `true` to turn this check off (default: `false`).

Omitting these keys from your YAML resets them to their defaults, which clears both allowlists. To restore the behavior of earlier AutoMDM versions while you migrate, set the [`server.allow_request_certificate_any_idp`](https://automdm.com/docs/configuration/automdm-server-configuration#server-allow-request-certificate-any-idp) server setting.

### certificate_authorities

_Available in AutoMDM Premium._

This section lets you configure your [certificate authorities (CA)](https://automdm.com/guides/connect-end-user-to-wifi-with-certificate) to help your end users connect to Wi-Fi and VPN.

#### Example

`default.yml`

```yaml
org_settings:
  certificate_authorities:
    digicert:
      - name: DIGICERT_WIFI
        url: https://one.digicert.com
        api_token: $DIGICERT_API_TOKEN
        profile_id: 926dbcdd-41c4-4fe5-96c3-b6a7f0da81d8
        certificate_common_name: $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART@example.com
        certificate_user_principal_names:
          - $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME
        certificate_seat_id: $AUTOMDM_VAR_HOST_HARDWARE_SERIAL
    ndes_scep_proxy:
      url: https://example.com/certsrv/mscep/mscep.dll
      admin_url: https://example.com/certsrv/mscep_admin/
      username: Administrator@example.com
      password: myPassword
    custom_scep_proxy:
      - name: SCEP_VPN
        url: https://example.com/scep
        challenge: $SCEP_VPN_CHALLENGE
    custom_est_proxy:
      - name: SECTIGO_WIFI
        url: https://example.com/.well-known/est/abc123
        username: $SECTIGO_USERNAME_PASSWORD
        password: $SECTIGO_WIFI_PASSWORD
    hydrant:
    - name: EST_WIFI
      url: https://example.com/est
      username: $EST_PROXY_USERNAME
      password: $EST_PROXY_PASSWORD
    hydrant:
      - name: HYDRANT_WIFI
        url: https://example.hydrantid.com/.well-known/est/abc123
        client_id: $HYDRANT_CLIENT_ID
        client_secret: $HYDRANT_CLIENT_SECRET
    smallstep:
      - name: SMALLSTEP_WIFI
        url: https://example.scep.smallstep.com/p/agents/integration-automdm
        challenge_url: https://example.scep.smallstep.com/xr9f4db7-83f1-48ab-8982-8b6870d4fl85/challenge
        username: $SMALLSTEP_USERNAME
        password: $SMALLSTEP_PASSWORD
    ejbca:
      - name: EJBCA_WIFI
        url: https://ejbca.example.com:8443
        client_p12: $EJBCA_CLIENT_P12
        password: $EJBCA_CLIENT_P12_PASSWORD
        trust_ca_bundle: $EJBCA_TRUST_CA_BUNDLE
        certificate_authority_name: ManagementCA
        certificate_profile_name: ENDUSER
        end_entity_profile_name: WIFI_DEVICES
        certificate_common_name: $AUTOMDM_VAR_HOST_HARDWARE_SERIAL
        username_template: $AUTOMDM_VAR_HOST_HARDWARE_SERIAL
        certificate_user_principal_names:
          - $AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME
```

#### digicert

- `name` is the name of certificate authority that will be used in variables in configuration profiles. Only letters, numbers, and underscores are allowed.
- `url` is the URL to DigiCert One instance (default: `https://one.digicert.com`).
- `api_token` is the token used to authenticate requests to DigiCert.
- `profile_id` is the ID of certificate profile in DigiCert.
- `certificate_common_name` is the certificate's CN. Accepts these AutoMDM variables: `$AUTOMDM_VAR_HOST_UUID`, `$AUTOMDM_VAR_HOST_HARDWARE_SERIAL`, `$AUTOMDM_VAR_HOST_PLATFORM`, `$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME`, `$AUTOMDM_VAR_HOST_END_USER_IDP_USERNAME_LOCAL_PART`, `$AUTOMDM_VAR_HOST_END_USER_IDP_FULL_NAME`, `$AUTOMDM_VAR_HOST_END_USER_IDP_GROUPS`, `$AUTOMDM_VAR_HOST_END_USER_IDP_DEPARTMENT`, `$AUTOMDM_VAR_HOST_END_USER_IDP_EXTERNAL_ID`, and the legacy `$AUTOMDM_VAR_HOST_END_USER_EMAIL_IDP`. Available on Apple hosts only.
- `certificate_user_principal_names` is the certificate's user principal names (UPN) attribute in Subject Alternative Name (SAN). Accepts the same AutoMDM variables as `certificate_common_name`.
- `certificate_seat_id` is the ID of the DigiCert's seat. Seats are license units in DigiCert. Accepts the same AutoMDM variables as `certificate_common_name`.

When `certificate_common_name`, `certificate_user_principal_names`, or `certificate_seat_id` is edited, certificates are reissued to every host with a configuration profile that uses this CA, across all teams. Editing only other fields, like `url` or `api_token`, doesn't reissue certificates. Certificates are also reissued when a host's IdP variable value changes.

Can only be configured for "All teams" (`org_settings`).

#### ndes_scep_proxy

Used to connect both Microsoft NDES and Okta CAs, since Okta uses NDES under the hood.

- `url` is the URL of the NDES SCEP endpoint (default: `""`).
- `admin_url` is the URL of the NDES admin endpoint (default: `""`).
- `username` is the username of the NDES admin endpoint (default: `""`).
- `password` is the password of the NDES admin endpoint (default: `""`).

Can only be configured for "All teams" (`org_settings`).

#### custom_scep_proxy

- `name` is the name of certificate authority that will be used in variables in configuration profiles. Only letters, numbers, and underscores are allowed.
- `url` is the URL of the Simple Certificate Enrollment Protocol (SCEP) server.
- `challenge` is the static challenge password used to authenticate requests to SCEP server.

#### custom_est_proxy

- `name` is the name of the certificate authority that will be used in variables in configuration profiles. Only letters, numbers, and underscores are allowed.
- `url` is the EST (Enrollment Over Secure Transport) endpoint's URL.
- `username` is the username used to authenticate with the EST endpoint.
- `password` is the password used to authenticate with the EST endpoint.

#### hydrant

_Available in AutoMDM Premium._

- `name` is the name of the certificate authority that will be used in variables in configuration profiles. Only letters, numbers, and underscores are allowed.
- `url` is the EST (Enrollment Over Secure Transport) endpoint provided by Hydrant.
- `client_id` is the client ID provided by Hydrant.
- `client_secret` is the client secret provided by Hydrant.

#### smallstep

- `name` is the name of the certificate authority that will be used in variables in configuration profiles. Only letters, numbers, and underscores are allowed.
- `url` is the **SCEP URL** from Smallstep.
- `challenge_url` is the **Webhook URL** from Smallstep.
- `username` is the **Challenge Basic Authentication Username** from Smallstep.
- `password` is the **Challenge Basic Authentication Password** from Smallstep.

#### ejbca

- `name` is the name of certificate authority that will be used in variables in configuration profiles. Only letters, numbers, and underscores are allowed. The name can't be `NDES` or the name of another certificate authority of any type, in any case.
- `url` is the URL of your EJBCA instance (for example, `https://ejbca.example.com:8443`). It must be an `https` URL without a username or password.
- `client_p12` is the base64-encoded PKCS#12 (`.p12`) file with the client certificate and private key that AutoMDM uses to authenticate to EJBCA with mutual TLS. To get the value, run `base64 -i client.p12`, and store the output in an environment variable.
- `password` is the password of the PKCS#12 file.
- `trust_ca_bundle` is the PEM-encoded CA certificates that sign the HTTPS certificate of your EJBCA instance: the root CA and any intermediate CAs (optional). The order doesn't matter. If it's empty, AutoMDM trusts public CAs.
- `certificate_authority_name` is the name of the CA in EJBCA that issues the certificates.
- `certificate_profile_name` is the name of the certificate profile in EJBCA.
- `end_entity_profile_name` is the name of the end entity profile in EJBCA.
- `certificate_common_name` is the certificate's CN. Accepts the same AutoMDM variables as `digicert`. Available on Apple hosts only.
- `username_template` is the username of the end entity that EJBCA creates for each host (optional). Accepts the same AutoMDM variables as `certificate_common_name`. Defaults to `certificate_common_name`.
- `certificate_user_principal_names` is the certificate's user principal names (UPN) attribute in Subject Alternative Name (SAN) (optional). Only one item is allowed. Accepts the same AutoMDM variables as `certificate_common_name`.

When GitOps adds or changes an EJBCA CA, AutoMDM checks the connection to EJBCA, so the client certificate must be valid and EJBCA must know the CA. AutoMDM doesn't return the client certificate or its password, so `automdmctl generate-gitops` adds placeholders for `client_p12` and `password` that you have to fill in.

When `certificate_authority_name`, `certificate_profile_name`, `end_entity_profile_name`, `certificate_common_name`, `username_template`, or `certificate_user_principal_names` is edited, certificates are reissued to every host with a configuration profile that uses this CA, across all teams. Editing only other fields, like `url` or `trust_ca_bundle`, doesn't reissue certificates. Certificates are also reissued when a host's IdP variable value changes.

Can only be configured for "All teams" (`org_settings`).

### microsoft_graph_credentials

_Available in AutoMDM Premium._

This section lets you connect AutoMDM to the Microsoft Graph API so that [Windows Autopilot](https://automdm.com/guides/windows-mdm-setup#windows-autopilot) devices appear in AutoMDM as pending hosts before they enroll.

The app registration needs the `DeviceManagementServiceConfig.Read.All` application permission, with admin consent granted for your tenant. AutoMDM currently supports one credential.

#### Example

`default.yml`

```yaml
org_settings:
  microsoft_graph_credentials:
    - tenant_id: 4e342a0d-ec1a-4353-bdeb-785542e0a8fb
      client_id: 122349c0-9b2c-4d3e-8f10-aabbccddeeff
      client_secret: $MICROSOFT_GRAPH_CLIENT_SECRET
```

- `tenant_id` is the Microsoft Entra tenant ID. Find your **Tenant ID** on [**Microsoft Entra ID** > **Home**](https://entra.microsoft.com/#home).
- `client_id` is the Microsoft Entra application (client) ID. Find your **Application (client) ID** on [**Microsoft Entra ID** > **App registrations**](https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade) > your MDM application > **Overview**.
- `client_secret` is the client secret for the app registration. Required when you add a credential, and when you change an existing credential's `tenant_id` or `client_id`. Omit it to keep the secret AutoMDM already stores. Find your **Client secret** on [**Microsoft Entra ID** > **App registrations**](https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade) > your MDM application > **Certificates & secrets**.

AutoMDM verifies each credential against Microsoft Graph before saving it, so GitOps fails with an error if the credential is wrong. Re-applying an unchanged credential makes no request to Microsoft and changes nothing in AutoMDM.

This section is declarative. Removing a credential from the list deletes it, and omitting `microsoft_graph_credentials` entirely deletes any credentials AutoMDM has stored.

Can only be configured for "All teams" (`org_settings`).

### webhook_settings

The `webhook_settings` section lets you define webhook settings for failing policy, vulnerability, and host status [automations](https://automdm.com/docs/using-automdm/automations).

- `interval` is how often policy webhooks/tickets and host status webhooks are triggered, formatted as number + unit of measurement (e.g. `"90m"`). Can be specified in seconds (`"s"`), minutes (`"m"`), or hours (`"h"`). (Default: `"24h"`)

#### activities_webhook

- `enable_activities_webhook` (default: `false`)
- `destination_url` is the URL to `POST` to when an activity is generated (default: `""`)

Can only be configured for "All teams" (`org_settings`). To send webhooks for a specific team's host activities, see [host_activities_webhook](#host-activities-webhook).

### Example

```yaml
org_settings:
  webhook_settings:
    activities_webhook:
      enable_activities_webhook: true
      destination_url: https://example.org/webhook_handler
```

#### host_activities_webhook

_Available in AutoMDM Premium._

- `enable_host_activities_webhook` (default: `false`)
- `destination_url` is the URL to `POST` to when an activity linked to one of the team's hosts is generated (default: `""`)

Can be configured for specific teams (`settings`) or "Unassigned" (`settings` in `unassigned.yml`). The webhook payload has the same format as [activities_webhook](#activities-webhook). MDM command results (shown via **Show MDM commands** on the host details page) are not returned via the activities API, and currently don't trigger this webhook. If `webhook_settings` is specified for "Unassigned" hosts and `host_activities_webhook` is omitted, the webhook is turned off.

### Example

```yaml
name: Workstations
team_settings:
  webhook_settings:
    host_activities_webhook:
      enable_host_activities_webhook: true
      destination_url: https://example.org/webhook_handler
```

#### failing_policies_webhook

> These settings can also be configured per-team when nested under `settings`.

- `enable_failing_policies_webhook` (default: `false`)
- `destination_url` is the URL to `POST` to when the condition for the webhook triggers (default: `""`).
- `policy_ids` is the list of policies that will trigger a webhook.
- `host_batch_size` is the maximum number of host identifiers to send in one webhook request. A value of `0` means all host identifiers with a failing policy will be sent in a single request.

#### Example

```yaml
org_settings:
  webhook_settings:
    failing_policies_webhook:
      enable_failing_policies_webhook: true
      destination_url: https://example.org/webhook_handler
      host_batch_size: 0
      policy_ids:
        - 1
        - 2
        - 3
```

#### host_status_webhook

- `enable_host_status_webhook` (default: `false`)
- `destination_url` is the URL to `POST` to when the condition for the webhook triggers (default: `""`).
- `days_count` is the number of days that hosts need to be offline to count as part of the percentage (default: `0`).
- `host_percentage` is the percentage of hosts that need to be offline to trigger the webhook. (default: `0`).

Can be configured for "All teams" (`org_settings`) and specific teams (`settings`).

#### Example

```yaml
org_settings:
  webhook_settings:
    host_status_webhook:
      enable_host_status_webhook: true
      destination_url: https://example.org/webhook_handler
      days_count: 7
      host_percentage: 25
```

#### vulnerabilities_webhook

- `enable_vulnerabilities_webhook` (default: `false`)
- `destination_url` is the URL to `POST` to when the condition for the webhook triggers (default: `""`).
- `host_batch_size` is the maximum number of host identifiers to send in one webhook request. A value of `0` means all host identifiers with a detected vulnerability will be sent in a single request.

Can only be configured for "All teams" (`org_settings`).

#### Example

```yaml
org_settings:
  webhook_settings:
    vulnerabilities_webhook:
      enable_vulnerabilities_webhook: true
      destination_url: https://example.org/webhook_handler
      host_batch_size: 0
```

### mdm

#### apple_business

_Available in AutoMDM Premium._

After [adding an Apple Business (AB) token via the UI](https://automdm.com/guides/macos-mdm-setup#apple-business-manager), the `apple_business` section lets you determine which team Apple hosts are assigned to in AutoMDM when they appear in Apple Business.

- `organization_name` is the organization name associated with the Apple Business account.
- `macos_team` is the team where macOS hosts are automatically added when they appear in Apple Business. If not specified, defaults to "Unassigned".
- `ios_team` is the the team where iOS hosts are automatically added when they appear in Apple Business. If not specified, defaults to "Unassigned".
- `ipados_team` is the team where iPadOS hosts are automatically added when they appear in Apple Business. If not specified, defaults to "Unassigned".
- `byod_team` is the team where BYOD hosts are automatically added when they are enrolled by an end user. If not specified, defaults to "Unassigned".
- `default` marks the AB token AutoMDM uses to verify Managed Apple Account sign-in on hosts that aren't in Apple Business. Only one AB token can be the default. If you have one AB token, it's always the default and you can leave this out.

Can only be configured for "All teams" (`org_settings`).

#### Example

```yaml
org_settings:
  mdm:
    apple_business:
    - organization_name: Fleet Device Management Inc.
      macos_team: 💻 Workstations
      ios_team: 📱🏢 Company-owned iPhones
      ipados_team: 🔳🏢 Company-owned iPads
      byod_team: 📱 BYOD iPhones
      default: true
```

#### windows_automatic_enrollment

_Available in AutoMDM Premium._

The `windows_automatic_enrollment` section lets you control the default team that new MDM enrolled Windows hosts get placed in.

- `default_team` specifies the name of the team that new MDM enrolled Windows hosts will automatically be added to (default: `""`).

#### Example

```yaml
org_settings:
  mdm:
    windows_automatic_enrollment:
      default_team: Windows Workstations
```

#### volume_purchasing_program

_Available in AutoMDM Premium._

After you've uploaded a [Volume Purchasing Program](https://automdm.com/guides/macos-mdm-setup#volume-purchasing-program-vpp) (VPP) token, the  `volume_purchasing_program` section lets you configure the teams in AutoMDM that have access to that VPP token's App Store apps. Currently, adding a VPP token is only available using AutoMDM's UI.
- `location` is the name of the organization unit in the Apple Business account. Apple previously called this "location." AutoMDM will rename it to "organization unit" in the next major version.
- `teams` is a list of team names. If you choose specific teams, App Store apps in this VPP account will only be available to install on hosts in these teams. If not specified, App Store apps will not be available to install on any team. To apply it to all teams, use `- All teams`. A VPP token can't be assigned to `All teams` and specific teams at the same time.

A team can have more than one VPP token. A VPP token assigned to `All teams` is available in every team, including teams created later, in addition to any other VPP tokens assigned to those teams. Learn how AutoMDM chooses the VPP token for each app in [multiple VPP tokens](#multiple-vpp-tokens).

When you remove a VPP token from a team, App Store apps that were added to that team from that VPP token are deleted from the team.

Can only be configured for "All teams" (`org_settings`).

#### Example

```yaml
org_settings:
  mdm:
    volume_purchasing_program:
    - location: Fleet Device Management Inc.
      teams:
      - 💻 Workstations
      - 💻🐣 Workstations (canary)
      - 📱🏢 Company-owned iPhones
      - 🔳🏢 Company-owned iPads
```

#### end_user_authentication

The `end_user_authentication` section lets you define the identity provider (IdP) settings used for [IdP authentication](https://automdm.com/guides/setup-experience#require-idp-authentication) during Automated Device Enrollment (ADE).

Once the IdP settings are configured, you can use the [`controls.setup_experience.enable_end_user_authentication`](#setup-experience) key to control the end user experience during ADE.

- `idp_name` is the human-friendly name for the identity provider that will provide single sign-on authentication (default: `""`).
- `entity_id` is the entity ID: a Uniform Resource Identifier (URI) that you use to identify AutoMDM when configuring the identity provider. It must exactly match the Entity ID field used in identity provider configuration (default: `""`).
- `metadata` is the metadata (in XML format) provided by the identity provider. (default: `""`)
- `metadata_url` is the URL that references the identity provider metadata. Only one of  `metadata` or `metadata_url` is required (default: `""`).

Can only be configured for "All teams" (`org_settings`):

#### Example

```
org_settings:
  mdm:
    end_user_authentication:
      entity_id: https://example.okta.com
      idp_name: Okta
      metadata: $END_USER_SSO_METADATA
      metadata_url: ""
```

##### end_user_license_agreement

You can require an end user to agree to an end user license agreement (EULA) before they can use their new Mac. `end_user_authentication` must be configured, and `controls.enable_end_user_authentication` must be set to `true`.

- `end_user_license_agreement` is the path to the PDF document.

Can only be configured for "All teams" (`org_settings`).

##### Example

```yaml
org_settings:
  mdm:
    end_user_license_agreement: ./lib/eula.pdf
```

##### windows_eula

You can require an end user to agree to terms and conditions before they can use their new Windows host.

- `windows_eula` is the path to the markdown file.

> Requires AutoMDM Premium and Windows MDM turned on. Otherwise, GitOps skips this key.
> Removing the key deletes the Windows agreement.

Can only be configured for "All teams" (`org_settings`).

##### Example

```yaml
org_settings:
  mdm:
    windows_eula: ./lib/windows-terms.md
```

##### apple_server_url

Update this URL if you're self-hosting AutoMDM and you want your hosts to talk to this URL for MDM features. (If not configured, hosts will use the base URL of the AutoMDM instance.)

If this URL changes and hosts already have MDM turned on, the end users will have to turn MDM off and back on to use MDM features.

Can only be configured for "All teams" (`org_settings`).

##### Example

```yaml
org_settings:
  mdm:
    apple_server_url: https://instance.automdm.com
```

#### yara_rules

The `yara_rules` section lets you define [YARA rules](https://virustotal.github.io/yara/) that will be served by AutoMDM's [authenticated
YARA rule](https://automdm.com/guides/remote-yara-rules) functionality.

Can only be configured for "All teams" (`org_settings`). To target rules to specific teams, target the reports referencing the rules to the desired teams.

##### Example

```yaml
org_settings:
  yara_rules:
    - path: ./lib/rule1.yar
    - path: ./lib/rule2.yar
```


#### smtp_settings

If you're self hosting AutoMDM, the `smtp_settings` section lets you configure an e-mail (SMTP) server. This enables AutoMDM to send user invite and password reset emails.

If you're using AutoMDM's managed-cloud offering, an SMTP server is already setup for you.

For possible options, see the parameters for the [smtp_settings object in the API](https://automdm.com/docs/rest-api/rest-api#smtp-settings).

Can only be configured for "All teams" (`org_settings`).

##### Example

```yaml
org_settings:
  smtp_settings:
    enable_smtp: true
    sender_address: organization@example.com
    server: localhost
    port: 1025
    authentication_type: none
```

Can only be configured for "All teams" (`org_settings`).

Unlike other options, omitting `smtp_settings` or leaving it blank won't reset the values back to the default.

## Tips

When renaming a team, first update the name in the UI, then update your YAML. If you only update the YAML, the team will be deleted and its hosts will lose their settings because they become "Unassigned".

Any settings not defined in your YAML files will be reset to the default values or deleted (e.g. software packages).

For the GitOps API token, create a dedicated API-only user with `automdmctl user create --api-only`. These users can modify configurations via GitOps but can’t access the AutoMDM UI. Assign the GitOps role and set the appropriate global or team scope in the UI.

`scripts`, `configuration_profiles`, `labels`, `policies`, and `reports` support both `path` (singular) and `paths` (plural).

- `path` references a single file path.
- `paths` accepts a wildcard ([glob pattern](https://code.visualstudio.com/docs/editor/glob-patterns)) to match multiple files at once (e.g. `../lib/windows/profiles/*.xml`).

Paths are always relative to the file you’re editing. You can't specify both `path` and `paths` on the same entry. Filenames containing `*`, `?`, `[`, or `{` can't be referenced using `path`. If your filenames contain these characters (e.g. a Windows configuration profile named `[AllowSpotlightCollection].xml`), either rename the files, or use `paths` with a wildcard pattern like `*.xml`.

<meta name="title" value="GitOps">
<meta name="description" value="Reference documentation for AutoMDM's GitOps workflow. See examples and configuration options.">
<meta name="pageOrderInSection" value="1500">
<meta name="keywordsForDocsearch" value="configuration as code, org settings, ci/cd, version control, declarative configuration">
