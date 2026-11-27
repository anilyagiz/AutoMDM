# Secrets and environment variables

The export never writes a secret value to disk. Each sensitive value becomes a `$VARIABLE` reference that `automdmctl gitops` fills from the environment (CI secrets or a local `.env`). `place` lists every variable in `.env.example`, in the README's "Environment variables" section, and in the GitHub workflow's `env:` block.

For the dry run, `validate` builds the values in memory and passes them only to the `automdmctl` process:

| Source | Meaning |
|---|---|
| **API** | Read from AutoMDM with a GET (the value is identical to what AutoMDM has, so the dry run compares like with like) |
| **Mask** | AutoMDM only returns `********` for this value. The dry run uses that mask, quoted so YAML doesn't read the `*` as an alias, and AutoMDM treats it as "keep the stored value". In CI the variable needs the real value. |
| **User** | AutoMDM never returns it. Taken from the repo's `.env` (or `--env-file`), then the environment; otherwise a placeholder, and the report says those files weren't checked with the real value. |

For masked values, a value in `.env` or the environment wins over the mask. Empty entries in `.env` count as unset.

| Value | Variable | Where | Dry-run source |
|---|---|---|---|
| AutoMDM server URL (`server_settings.server_url`) | `AUTOMDM_URL` (the template's) | `default.yml` | API |
| Global enroll secrets | `AUTOMDM_GLOBAL_ENROLL_SECRET`, `_2`, … | `default.yml` `org_settings.secrets` | API (`/spec/enroll_secret`) |
| AutoMDM enroll secrets | `AUTOMDM_<AUTOMDM>_ENROLL_SECRET`, `_2`, … | automdm file `settings.secrets` | API (`/teams/{id}`) |
| Webhook destination URLs (activities, host status, failing policies, vulnerabilities, host activities) | `AUTOMDM_<KIND>_URL`, `AUTOMDM_<AUTOMDM>_<KIND>_URL` | `webhook_settings` | API |
| Google Calendar webhook URL (per team) | `AUTOMDM_<AUTOMDM>_CALENDAR_WEBHOOK_URL` | team `settings.integrations` | API |
| macOS migration webhook URL | `AUTOMDM_MACOS_MIGRATION_WEBHOOK_URL` | `controls.macos_migration` | API |
| Admin SSO IdP metadata XML | `AUTOMDM_SSO_METADATA` | `org_settings.sso_settings.metadata` | API |
| End-user authentication IdP metadata XML | `AUTOMDM_MDM_SSO_METADATA` | `org_settings.mdm.end_user_authentication.metadata` | API |
| Jira and Zendesk API tokens | `AUTOMDM_JIRA_API_TOKEN`, `AUTOMDM_ZENDESK_API_TOKEN`, `_2`, … | `org_settings.integrations` | Mask |
| Google Calendar and Google Workspace service account keys | `AUTOMDM_GOOGLE_CALENDAR_API_KEY_JSON`, `AUTOMDM_GOOGLE_WORKSPACE_API_KEY_JSON` | `org_settings.integrations` | Mask |
| Certificate authority secrets (DigiCert token, NDES password, SCEP challenge, Hydrant client secret, EST password, Smallstep username and password, Google CAS key) | `AUTOMDM_CA_<NAME>_<FIELD>` | `org_settings.certificate_authorities` | API (`include_secrets=true`) |
| EJBCA client PKCS#12 and password | `AUTOMDM_CA_<NAME>_CLIENT_P12`, `AUTOMDM_CA_<NAME>_PASSWORD` | `org_settings.certificate_authorities.ejbca` | Mask (AutoMDM can't export them; reported as a gap) |
| Apple account provisioning OAuth client secret | `AUTOMDM_APPLE_ACCOUNT_PROVISIONING_CLIENT_SECRET` | `controls.apple_account_provisioning` | Mask |
| Microsoft Graph client secret | — (left out; AutoMDM keeps the stored secret when the key is absent) | — | — |
| Credentials in agent options and features: values under a key named like a secret, password, token, or key (AWS keys for the osquery loggers, say), and values holding an enroll secret | `AUTOMDM_AGENT_OPTIONS_<KEY>`, `AUTOMDM_<AUTOMDM>_AGENT_OPTIONS_<KEY>`, `AUTOMDM_FEATURES_<KEY>`, … | `agent_options`, `features` | API (AutoMDM returns these unmasked) |
| Package download URLs with credentials (`user:pass@`, or a token, key, signature, or auth query parameter) | `AUTOMDM_<TITLE>_URL`, `_2`, … | the package YAML's `url` | API (`/software/titles/{id}`) |
| Custom variables in scripts and profiles | `AUTOMDM_SECRET_<NAME>` (name kept) | the script or profile | User |

`automdmctl gitops` puts each value into the YAML as-is (`key: $NAME`), so a value with YAML syntax in it (`: `, ` #`, a leading `*`, `&`, `!`, or `%`) or one that reads as a number, boolean, or null applies as something else. `validate` parses each value the way it would land. A value from AutoMDM that doesn't survive gets a WARN, and the dry run uses it wrapped in double quotes, which is how it has to be set in CI too (with `\` and `"` escaped). A value from `.env` or the environment that doesn't survive is a FAIL; quote it there and in CI. Multi-line metadata and JSON service account keys are meant to go in raw, and `AUTOMDM_SECRET_*` values aren't put into YAML, so they're skipped. A value that a profile or DDM asset also uses can't be quoted for both, so that's a FAIL: change it in AutoMDM to one that's plain text in YAML.

`SSO_METADATA` values are multi-line XML. `automdmctl gitops` expands variables before parsing YAML, so `place` uncomments the matching lines in `.github/automdm-gitops/gitops.sh` that indent the value (and fixes the template's copy, which reads `AUTOMDM_MDM_SSO_METADATA` for both). `validate` applies the same indentation in memory.

## Enroll secrets inside profiles

A GitOps repo often puts an enroll secret in a profile (an automdmd configuration profile's `EnrollSecret`, say) as `$VARIABLE`, and AutoMDM stores the profile after `automdmctl` expanded it. The collector finds each enroll secret in profiles, DDM assets, activations, scripts, the setup experience script, and software scripts and pre-install queries while it still has the values, and keeps only the span or which secret it is. The planner writes that secret's variable (`$AUTOMDM_GLOBAL_ENROLL_SECRET`, `$AUTOMDM_<AUTOMDM>_ENROLL_SECRET`) in its place in profiles and DDM assets, which `automdmctl` expands back to the same bytes, and records the variable on the file in `plan.json` (`env_refs`). Reconcile fills the variable in before comparing hashes; with `validate --no-dry-run` it has no values, so it warns that those files weren't compared.

Scripts, the setup experience script, software scripts, and DDM activations aren't expanded by `automdmctl`, so an enroll secret in one could only be exported as-is. The script, setup script, software item, or profile (for its activation) is left out instead, with a `GAP` line (reconcile reports a left-out script, profile, or software item as missing), until it's fixed in AutoMDM: in a script, replace the value with a `$AUTOMDM_SECRET_` variable, which AutoMDM fills in when it runs the script; elsewhere, take the secret out; then export again. Enroll secrets shorter than eight characters match ordinary text too often to replace. A profile, DDM asset, script, setup script, or software item that holds one as a whole word is left out with a `GAP` line (change the secret in AutoMDM to a longer one, then export again), and one that holds it only inside a longer word gets a `WARN` line.

## Making sure none get through

The collector also matches each enroll secret escaped for JSON (including `\/`), URL-encoded, XML-escaped, and inside base64 `<data>` payloads. automdmctl would insert the plain value for a variable, so a file holding one of those forms is left out rather than rewritten. Items written as YAML (policies, reports, labels, profile metadata) are left out the same way. The plan's inventory, which reconcile compares against, keeps no values for items left out because of a secret, and leaves variable-held settings out, so `plan.json` holds no secret either. As a last check, `validate` searches every repo file for the secret values it fetched for the dry run and fails on any it finds. Webhook URLs and IdP metadata are variables too but aren't searched for, since a script or profile may hold the same URL.

## Not treated as secrets

- SSO and end-user authentication `metadata_url`, entity IDs, IdP names: the template writes them in plain text.
- Other values inside profiles and scripts. They're exported exactly as AutoMDM stores them; `$AUTOMDM_SECRET_*` placeholders stay placeholders. If a profile or script has a credential written into it, flag it to the user rather than changing the file (changing it would change what AutoMDM deploys).
- GitOps exceptions: when AutoMDM marks enroll secrets as managed in the UI (**Settings > Integrations > Change management**), no `secrets:` keys are written, because automdmctl fails when they're present. A fresh AutoMDM instance has this exception turned on.
