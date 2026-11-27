# Releasing AutoMDM

This section outlines the release process at AutoMDM. The current release cadence is one minor and one patch release every three weeks.

## Script release

All AutoMDM releases are completed using the [AutoMDM releaser script](https://github.com/anilyagiz/automdm/blob/main/tools/release/README.md).

## Manual release

If necessary, manual release instructions are preserved below. 

### Prepare a new version of AutoMDM

Note: Please prefix versions with `automdm-v` (e.g., `automdm-v4.0.0`) in git tags, Helm charts, and NPM configs.

1. Update the [CHANGELOG](https://github.com/anilyagiz/automdm/blob/main/CHANGELOG.md) with the changes you made since the last AutoMDM release. Use `make changelog` to pull the change files into `CHANGELOG.md`, then manually edit. When editing, order the most relevant/important changes at the top and make sure each line is in the past tense. `make changelog` will stage all change file entries for deletion with the commit.

2. Update version numbers in the relevant files:

- [automdmctl package.json](https://github.com/anilyagiz/automdm/blob/main/tools/automdmctl-npm/package.json) (do not yet `npm publish`)
- [Helm chart.yaml](https://github.com/anilyagiz/automdm/blob/main/charts/automdm/Chart.yaml) and [values file](https://github.com/anilyagiz/automdm/blob/main/charts/automdm/values.yaml)
- GCP Terraform [variables](https://github.com/anilyagiz/automdm/blob/main/infrastructure/dogfood/terraform/gcp/variables.tf)
- Kubernetes [Helm chart](https://github.com/anilyagiz/automdm/blob/main/charts/automdm/Chart.yaml)
- All Terraform (*.tf) files referencing the previous version of AutoMDM.
- The full list can be found by using git grep:
    % git grep "4\.3\.0"

Commit these changes via Pull Request and pull the changes on the `main` branch locally.

### Prepare a minor or major release

1. Complete the steps above to [prepare a new version of AutoMDM](#prepare-a-new-version-of-team).

2. Create a new branch. Minor or major release branches should be prefixed with `prepare-`. In this example we are creating `v4.3.0`:

```sh
git checkout main
git checkout --branch prepare-automdm-v4.3.0
```

3. [Create release candidate](#create-release-candidate). 

4. [Complete release QA](#complete-release-qa). 

5. Tag and push the new release:

```sh
git tag automdm-v<VERSION>
git push origin automdm-v<VERSION>
git tag v<VERSION>
git push origin v<VERSION>
```

> For example, `git tag automdm-v4.75.0` and `git tag v4.75.0`.

Note that `origin` may be `upstream` depending on your `git remote` configuration. The intent here
is to push the new tag to the `github.com/anilyagiz/automdm` repository.

After the tag is pushed, GitHub Actions will automatically begin building the new release.

***

Wait while GitHub Actions creates and uploads the artifacts.

***

When the Actions Workflow has been completed, [publish the new version of AutoMDM](#publish-a-new-version-of-team).

### Prepare a patch release

We issue scheduled patch releases every Monday between minor releases if any bug fixes have merged. We issue patches immediately for critical bugs as defined in [our handbook](https://automdm.com/handbook/quality#critical-bugs).

1. Create a new branch, starting from the git tag of the prior release. Patch branches should be prefixed with `patch-`. In this example we are creating `v4.3.1`:
   
```sh
git checkout automdm-v4.3.0
git checkout --branch patch-automdm-v4.3.1
```

2. Cherry picks the necessary commits from `main` into the new branch:
  
```sh
git cherry-pick d34db33f
```

3. Complete the steps above to [prepare a new version of AutoMDM](#prepare-a-new-version-of-team).

> Commits must be cherry-picked in the order they appear on `main` to avoid conflicts. Make sure to also cherry-pick the commit containing changelog and version number updates.

4. **Important!** Any migrations that are not cherry-picked in a patch must have a _later_ timestamp than migrations that were cherry-picked. If there are new migrations that were not cherry-picked, verify that those migrations have later timestamps. If they do not, submit a new Pull Request to increase the timestamps and ensure that migrations are run in the appropriate order.

5. [Create release candidate](#create-release-candidate). 

6. [Complete release QA](#complete-release-qa). 

7. Tag and push the new release in Git:
  
```sh
git tag automdm-v4.3.1
git push origin automdm-v4.3.1
```

Note that `origin` may be `upstream` depending on your `git remote` configuration. The intent here
is to push the new tag to the `github.com/anilyagiz/automdm` repository.

After the tag is pushed, GitHub Actions will automatically begin building the new release.

***

Wait while GitHub Actions creates and uploads the artifacts.

***

When the Actions Workflow has been completed, [publish the new version of AutoMDM](#publish-a-new-version-of-team).

### Create release candidate

1. Push a branch containing new commits to [automdm/automdm](https://github.com/anilyagiz/automdm) that begins with `prepare-*` or `patch-*`. 
   ```sh
   git push origin patch-automdm-v4.3.1
   ```

   > When a `prepare-*` or `patch-*` branch is pushed, the [Docker publish Action](https://github.com/anilyagiz/automdm/actions/workflows/goreleaser-snapshot-automdm.yaml) will run and create a container image for QA with `automdmctl preview` (eg. `automdmctl preview --tag patch-automdm-v4.3.1`).

2. Check the [Docker Publish GitHub action](https://github.com/anilyagiz/automdm/actions/workflows/goreleaser-snapshot-automdm.yaml) to confirm it completes successfully for this branch.

3. Create a [Release QA](https://github.com/anilyagiz/automdm/blob/main/.github/ISSUE_TEMPLATE/release-qa.md) issue. Populate the version and browsers, and assign to the QA person leading the release. Add the appropriate [product group label](https://automdm.com/handbook/company/product-groups), and `:release` label, so that it appears on the product group's release board.

4. Notify QA that the release candidate is ready for (release QA)[#complete-release-qa].

### Complete release QA

1. Move the release QA issue into the "In progress" column on the release board. 

2. Complete each item listed in the release QA issue. 

3. If bugs are found, file bug tickets and notify your EM.

4. When all items are completed with no bugs remaining, move the issue to the "Ready for release" column on the release board and notify your EM.

### Publish a new version of AutoMDM

1. Edit the draft release on the [GitHub releases page](https://github.com/anilyagiz/automdm/releases).Use the version number as the release title. Use the below template for the release description
(replace items in <> with the appropriate values):
   
```md
### Security Engineers

<COPY FROM CHANGELOG>

### IT Admins

<COPY FROM CHANGELOG>

> AutoMDM-maintained app updates and vulnerability feed fixes are applied, whether or not you upgrade.

### AutoMDM's agent and automdmctl CLI

The following version of AutoMDM's agent (automdmd) and automdmctl support the latest changes to AutoMDM:

<UPDATE VERSIONS AND LINKS BELOW>
1. [orbit-v1.x.x](https://github.com/fleetdm/fleet/releases/tag/orbit-v1.x.x)
2. automdm-desktop-v1.x.x (included with Orbit)
3. osquery-x.x.x
4. [automdmd-chrome-v1.x.x](https://github.com/fleetdm/fleet/releases/tag/fleetd-chrome-v1.x.x)
5. automdmctl vx.x.x

> While newer versions of automdmd and automdmctl still function with older versions of the AutoMDM server (and vice versa), AutoMDM does not actively test these scenarios and some newer features won't be available.

### Upgrading

Please visit our [upgrade guide](https://automdm.com/docs/deploying/upgrading-automdm) for upgrade instructions.

### Documentation

Documentation for AutoMDM is available at [automdm.com/docs](https://automdm.com/docs).

### Binary Checksum

**SHA256**

<COPY FROM checksums.txt>
```

When editing is complete, publish the release.

2. Publish the new version of `automdmctl` on NPM. Run `npm publish` in the [automdmctl-npm](https://github.com/anilyagiz/automdm/tree/main/tools/automdmctl-npm) directory. Note that NPM does not allow replacing a package without creating a new version number. Take care to get things correct before running `npm publish`!

> If releasing a "prerelease" of AutoMDM, run `npm publish --tag prerelease`. This way, you can publish a prerelease of automdmctl while the most recent automdmctl npm package, available for public download, is still the latest _official_ release.

3. Deploy the new version to AutoMDM's internal dogfood instance: https://automdm.com/handbook/engineering#deploying-to-dogfood.

4. In the #help-infrastructure Slack channel, notify the @infrastructure-oncall of the release. The @infrastructure-oncall will schedule time to upgrade our managed cloud to the new version.

5. Announce the release in the #team channel of [osquery Slack](https://automdm.com/slack) by updating the channel topic with the link to this release. 

6. Announce the release in the #general channel by copying and pasting the osquery Slack channel topic.
