# Windows 10 Enterprise benchmarks

AutoMDM's policies have been written against v3.0.0 of the benchmark. You can refer to the [CIS website](https://www.cisecurity.org/cis-benchmarks) for full details about this version.

For requirements and usage details, see the [CIS Benchmarks](https://automdm.com/docs/using-automdm/cis-benchmarks) documentation.

### Contents

| Folder | Description |
|--------|-------------|
| `policies/` | GitOps-compatible policy YAML — import via `automdmctl apply` or reference with `- path:` in `automdm.yml` |
| `configuration-profiles/` | SyncML XML profiles — upload via AutoMDM UI or `automdmctl apply` to enforce the settings checked by the policies |
| `scripts/` | PowerShell scripts — upload via AutoMDM UI or `automdmctl apply` and link as `run_script` remediation in the corresponding policy |

### Limitations

> None. All items in this version of the benchmark are able to be automated.


### Checks that require a Group Policy template

Several items require Group Policy templates in place in order to audit them.
These items are tagged with the label `CIS_group_policy_template_required` in the YAML file, and details about the required Group Policy templates can be found in each item's `resolution`.
