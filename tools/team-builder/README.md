> [!NOTE]
> **Prefer [`dibble`](../dibble/README.md) for seeding teams.** The equivalent is:
> ```bash
> ./tools/dibble/dibble teams --count N
> ```
> This tool is kept for backwards compatibility (it also generates installer
> packages, which dibble does not). We'll remove it once no one references it.

# AutoMDM team builder

Using a list of teams (formerly "teams") in a file as input, adds the listed teams to AutoMDM and generates installer processes. 

For each team, an enroll secret will be created, the team added to AutoMDM using the team yaml template, and `.msi`,`.deb`, `.pkg` and `.rpm` installer packages will be created. 

## Requirements

[automdmctl](https://automdm.com/docs/using-automdm/automdmctl-cli)
Docker (for generating Windows installers)

## Flags 

Required flags:

- -s: The source file containing teams to be added. 
- -u: The url of the AutoMDM server.

Optional flags:

- -p: packages - Default: "all" - The types of installer packages to create for each team.
- -f: flags - Additional flags to apply to `automdmctl package`.
- -o: output - Default: Current location - Directory in which to place the generated packages.
- -x: dry_run - Test prossesing the file, creating the team in AutoMDM, and generating packages without applying any changes to the server.

## Usage

1. Install and log in to automdmctl

2. Install and start Docker

3. Create a file including a list of teams, one per line:

```
Workstation
Canary
Servers
```
4. Run the script and pass the AutoMDM Server URL and source file as arguments:

```console
$ ./build_teams.sh -s teams.txt -u automdm.org.com
```

## Team configuration

The teams generated with this script will use your global agent options. You can apply [team settings](https://automdm.com/docs/using-automdm/configuration-files#team-settings) after the team has been created. 

## Testing

To test team creation and package generation without applying the changes to AutoMDM, include the `-x` flag. This will add the `--dry_run` flag to `automdmctl apply`. All actions will be taken, but the generated team configuration YAML will be validated without creating the new team. 

