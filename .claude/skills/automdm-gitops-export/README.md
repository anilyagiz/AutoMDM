# Export your AutoMDM to GitOps

This skill lets an AI coding agent turn the AutoMDM you've configured in the UI into a GitOps repository. You end up with the same folders and files `automdmctl new` creates, filled in with your real settings, teams, profiles, scripts, policies, reports, labels, and software. Secrets become environment variables, and a read-only `automdmctl gitops --dry-run` proves that applying the repository would change nothing.

The export only reads from AutoMDM. It doesn't change your AutoMDM instance, and it doesn't apply the repository. You do that later, through CI, after you've reviewed it.

It replaces `automdmctl generate-gitops`. [references/parity-checklist.md](references/parity-checklist.md) compares the two.

## Before you start

You need:

- An AutoMDM user with the global admin role.
- `automdmctl` 4.84.0 or later, ideally the same version as your AutoMDM server.
- Python 3.9 or later. On Windows, run it as `py -3` or `python`.
- An AI coding agent that can run commands: Claude Code, Codex, Cursor, GitHub Copilot, Gemini CLI, or Kilo Code.
- About 15 minutes, plus a few minutes per team to review the result.

If you're trying the skill out, point it at a test or staging AutoMDM first.

## Step 1: Install automdmctl and log in

If you skip this step, the agent helps when you start the export. It installs `automdmctl` and sets your AutoMDM address, after asking you, and then asks you to log in in your own terminal, so your password or API token never goes through the agent.

1. Install `automdmctl`:

   ```bash
   npm install -g automdmctl
   ```

   To match your AutoMDM server's version, add it to the package name, for example `automdmctl@4.94.0`. [Other ways to install automdmctl](https://automdm.com/guides/automdmctl#installing-automdmctl).

2. Point `automdmctl` at your AutoMDM server and log in:

   ```bash
   automdmctl config set --address https://automdm.example.com
   automdmctl login
   ```

   If you sign in to AutoMDM with SSO or two-factor authentication, `automdmctl login` won't work. Get an API token from your account page in the AutoMDM UI, then run `automdmctl config set --token <token>` instead. [Learn more](https://automdm.com/guides/automdmctl#users-with-single-sign-on-sso-or-email-two-factor-authentication-2-fa).

3. Check that it works:

   ```bash
   automdmctl get config
   ```

## Step 2: Add the skill to your agent

The skill is this folder: `SKILL.md` plus `scripts/` and `references/`.

- Working in the AutoMDM repository: there's nothing to do. Claude Code finds it in `.claude/skills/`, and Codex, Cursor, Copilot, Gemini CLI, and Kilo Code find it through `.agents/skills/automdm-gitops-export/`.
- Anywhere else: download this folder:

  ```bash
  git clone --depth 1 --filter=blob:none --sparse https://github.com/anilyagiz/automdm.git automdm-skill
  git -C automdm-skill sparse-checkout set .claude/skills/automdm-gitops-export
  ```

  Then copy `automdm-skill/.claude/skills/automdm-gitops-export` into `~/.claude/skills/` for Claude Code, or into `.agents/skills/` in the folder where you start Codex, Copilot, Gemini CLI, or Cursor. Some agents ask you to trust a skill the first time they load it.

## Step 3: Ask your agent to export

1. Open a terminal in the folder where the new repository should live, and start your agent.
2. Ask it, for example:

   > Export our AutoMDM to a GitOps repo in ./automdm-gitops

   In Claude Code, you can also type `/automdm-gitops-export ./automdm-gitops`.

3. Answer the agent's questions:
   - Is this the right AutoMDM? It shows the server URL `automdmctl` is logged in to. Say yes only if it's the AutoMDM you mean to export.
   - Organization name: it suggests the one AutoMDM has.
   - Folder: it has to be empty, unless you agree to the agent writing into it.

The agent creates the layout, reads everything from AutoMDM, and writes a plan. This takes a minute or two on most instances, and longer with hundreds of profiles or software titles.

## Step 4: Answer the review questions

AutoMDM doesn't record a few things, so the agent checks them with you before it writes the files. You might see:

- Which platform a script is for. AutoMDM runs `.sh` and `.py` scripts on macOS and Linux and doesn't record which one a script targets. The agent reads each script and asks only when it can't tell.
- Apple profiles in a mobile team. AutoMDM stores one Apple platform for macOS, iOS, and iPadOS, so the agent confirms where those profiles go.
- AutoMDM-maintained app scripts. AutoMDM supplies these apps' install and uninstall scripts and keeps them current unless someone edits them. The API doesn't say which ones were edited, so when a stored script looks like an older copy of AutoMDM's, the agent asks whether anyone changed it. If someone did, the export keeps it. Otherwise, AutoMDM keeps supplying the current one.

The agent also lists gaps: things GitOps can't represent, such as bootstrap packages, users, and API tokens. They stay as they are in AutoMDM.

## Step 5: Add secret values for the check (optional)

AutoMDM never returns some values: custom variables (`$AUTOMDM_SECRET_...`) and tokens it shows only masked. To check the files that use them with their real values:

1. In the new repository, copy `.env.example` to `.env`.
2. Fill in the values you want checked, and leave the rest empty.

Git ignores `.env`, and the agent doesn't open it, so these values never pass through the agent. If you skip this step, the check still runs and tells you which files it couldn't check with real values.

## Step 6: Read the result

The agent validates the repository and reports:

- PASS or FAIL. PASS means every file reference resolves, the repository matches AutoMDM item by item, and the dry run would change nothing.
- What was exported, for each team.
- Gaps, and why GitOps can't carry them.
- Environment variables to set in CI. `.env.example` and the repository's README list them too.

Read through the files before you commit them.

## Step 7: Turn on GitOps in CI

The repository includes a GitHub Actions workflow and a GitLab pipeline. On GitHub, the workflow does a dry run for pull requests. It applies the repository to AutoMDM when you push to `main`, when you run it by hand, and every night on a schedule. Add the secrets before anything reaches `main`:

1. Create an API-only user for CI with the GitOps role (on AutoMDM Free, use the global admin role). The command prints the user's API token.

   ```bash
   automdmctl user create --name "GitOps" --api-only --global-role gitops
   ```

   [Learn more about API-only users](https://automdm.com/guides/automdmctl#create-api-only-user).

2. Create an empty, private repository on GitHub or GitLab.
3. Add these as repository secrets (GitHub) or CI/CD variables (GitLab):
   - `AUTOMDM_URL`: your AutoMDM server's URL.
   - `AUTOMDM_API_TOKEN`: the API-only user's token.
   - Every variable in `.env.example`, with its real value. If the check said a value has to be quoted, wrap it in double quotes.
4. Push the repository to a branch other than `main`, and open a pull request. Check that the workflow's dry run passes and shows nothing you didn't expect.
5. Merge the pull request. From now on, merging to `main` applies the repository to AutoMDM.
6. In AutoMDM, go to **Settings > Integrations > Change management** and turn on **GitOps mode**. Otherwise, the next run overwrites changes made in the AutoMDM UI.

## Step 8: Clean up

Ask the agent to clean up, or run `python3 <skill folder>/scripts/gitops_export.py clean --dir ./automdm-gitops`. This deletes the export's work folder, which holds copies of your profiles and scripts outside the repository. Delete `.env` too if you don't need it.

## Troubleshooting

- "isn't a global admin": log in to `automdmctl` as a user with the global admin role.
- Version warning: `automdmctl` and your AutoMDM server are different versions. Install the matching `automdmctl` (step 1) so the dry run behaves the way CI's will.
- "can't reach AutoMDM" though automdmctl works in your terminal: your agent runs commands in a sandbox without network access. Allow network access for these commands.
- "AutoMDM's default install and uninstall scripts couldn't be read": outside the AutoMDM repository, the export downloads [those scripts](https://github.com/anilyagiz/automdm/tree/main/pkg/file/scripts) from `raw.githubusercontent.com` to tell AutoMDM's defaults from your own scripts. Allow access to that address and ask the agent to export again.
- `unknown key "org_settings.yara_rules..."`: a known `automdmctl` bug. GitOps can't carry YARA rules until it's fixed.
- Dry-run notes about placeholders or masks: those values weren't in `.env`, so the files that use them weren't checked with real values. Add them (step 5) and ask the agent to validate again.
- Anything else: ask the agent what failed. Every step is safe to run again. [Talk to a human at AutoMDM](https://automdm.com/support) if you're stuck.

## Run the scripts without an agent

```bash
python3 scripts/gitops_export.py all --dir ~/automdm-gitops --org-name "Acme, Inc."
```

`all` runs `preflight`, `scaffold`, `export`, `place`, `validate`, and `report` in a row, and exits non-zero when validation fails. Run them one at a time to review the plan before anything is written. [SKILL.md](SKILL.md) describes each step.

## Tests

```bash
python3 -m unittest discover -s scripts/tests
```

The tests run offline. Inside the AutoMDM repository, they also check the templates `automdmctl new` ships, AutoMDM's default package scripts in `pkg/file/scripts/`, and the list of deprecated GitOps keys. Outside it, stand-ins take the place of the default scripts.
