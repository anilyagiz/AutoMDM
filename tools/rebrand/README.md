# Fleet to AutoMDM rebrand

AutoMDM is built from [fleetdm/fleet](https://github.com/fleetdm/fleet). This directory holds the tools that turn an
upstream checkout into the AutoMDM tree.

## What is in the history

| Commit | What it is |
| --- | --- |
| `Import fleetdm/fleet at ...` | The unmodified upstream snapshot, without `website/`, `handbook/`, `articles/` and `it-and-security/`. |
| `Rebrand Fleet as AutoMDM (mechanical pass)` | Output of `rebrand.py`, then `gofmt`. No hand edits. |
| Later commits | Hand fixes the script cannot make, each with its reason in the commit message. |

`git diff <snapshot> <mechanical>` shows exactly what the script changed; every later commit is a reviewed fix-up.

## Files

- `rebrand.py`: the rename. Run `python3 rebrand.py --help`.
- `phrases.py`: rules for the word "fleet" in prose, such as "your fleet of laptops" (kept) versus "the Workstations
  fleet" (a team).
- `overrides.tsv`: single words the rules decide wrongly, by original path, line and position on the line.
- `typecheck/`: type-checks every Go package, tests included, and keeps going past broken packages.

## What the script renames

| Upstream | AutoMDM |
| --- | --- |
| Fleet, fleet, FLEET, FleetDM | AutoMDM, automdm, AUTOMDM |
| `fleetctl`, `fleetd`, Fleet Desktop | `automdmctl`, `automdmd`, AutoMDM Desktop |
| `FLEET_*` environment variables, `$FLEET_VAR_*`, `$FLEET_SECRET_*` | `AUTOMDM_*`, `$AUTOMDM_VAR_*`, `$AUTOMDM_SECRET_*` |
| `/api/v1/fleet/...` | `/api/v1/automdm/...` |
| `github.com/fleetdm/fleet/v4` | `github.com/anilyagiz/automdm/v4` |
| Fleet 5's "fleet" meaning a group of hosts (`fleet_id`, `/fleets`, `--fleet`) | team (`team_id`, `/teams`, `--team`) |

Where renaming a Go or TypeScript identifier such as `FleetID` to `TeamID` would collide with an identifier that already
exists, the script uses the brand instead (`AutoMDMID`). The report lists these.

## What the script keeps

- Links to upstream issues, pull requests and commits.
- Other repositories and images of the fleetdm organization, including the CPE, OVAL and vulnerability feeds.
- Service hosts the product calls: `updates.fleetdm.com`, `download.fleetdm.com`, `maintained-apps.fleetdm.com`,
  `fleetdm.com/api/...`. Pass `--rename-service-endpoints` once AutoMDM hosts its own.
- Reverse-DNS identifiers (`com.fleetdm.*`): Apple payload identifiers, bundle IDs, the Android application ID.
  Renaming them changes the profiles on enrolled devices. Pass `--rename-reverse-dns` to rename them anyway.
- `LICENSE*` files, `go.sum`, `yarn.lock`, `CHANGELOG.md` and `changes/`.

## Hand fixes after the mechanical pass

The mechanical pass turns Fleet 5's teams-to-fleets compatibility layer into teams-to-teams rules. The later commits
remove those rules (`pkg/removednames`, `pkg/spec`, the removed API paths in `server/service/handler_removed_paths.go`,
the CLI's removed flags and commands), make migration `20261111205515` a no-op because its copy-then-delete would erase
the ABM and VPP team assignments, and map the collision-renamed front-end parameters (`automdmId`) back to `team_id`
when they are sent to the API. They also fix tests whose fixtures were built upstream.

## Updating to a newer upstream

1. Check out the new upstream version.
2. Run the script into an empty directory:
   `python3 tools/rebrand/rebrand.py UPSTREAM OUT --overrides tools/rebrand/overrides.tsv --report report.txt --log decisions.tsv`
3. Read `report.txt`: "code identifiers that now share one name" must be empty, and "remaining 'fleet' strings" should
   only list kept names.
4. Commit the new upstream snapshot, then the script output, then cherry-pick the hand-fix commits and resolve conflicts.
5. Push a `rebrand/**` branch: `.github/workflows/automdm-verify.yml` runs the build and the test suites on both the
   snapshot and the rebranded tree, so new failures stand out.
