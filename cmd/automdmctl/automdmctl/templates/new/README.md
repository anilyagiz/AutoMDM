# AutoMDM

These files allow you to configure, patch, and secure computing devices for your organization.

Whether you're making changes by hand or spinning them up from Slack or Teams using a tool like Claude or Kilo Code _(e.g. "Make our endpoints compliant with ISO 27001" or "Fix CVE-2026-XXXX")_, your team reviews, merges, and it deploys to thousands of endpoints in seconds. This makes it straightforward to instantly rollback a change, and history is fully tracked.

You can read more about the anatomy of these files and what they do in [AutoMDM's documentation](https://automdm.com/docs/configuration/yaml-files). You can also opt to manage particular aspects of AutoMDM in the graphical user interface _instead_, such as software versions or labels.

> Unsure? Talk to a human at automdm.com/support

## How to use

1. Install automdmctl. [Learn how](https://automdm.com/guides/automdmctl#installing-automdmctl).

2. Open your Terminal, run `automdmctl new`, and follow instructions in the output.

## Use AI

This repository includes an `AGENTS.md` and an `automdm-gitops` skill so AI coding agents can write policies, reports, software, and configuration profiles for you and validate them before a pull request. The skill lives in `.agents/skills/`, where Codex, Cursor, GitHub Copilot, Gemini CLI, and Kilo Code discover it; Claude Code finds it through `.claude/skills/` and reads `AGENTS.md` through `CLAUDE.md`. Some agents ask you to trust the repository's skills the first time. See [Use Claude Code with AutoMDM](https://automdm.com/guides/use-claude-code-with-team) to get started, with or without GitOps.

## Tips

The action (GitHub) or pipeline (GitLab) runs will fail until you add `AUTOMDM_URL` and `AUTOMDM_API_TOKEN` as [secrets (GitHub)](#github) or [CI/CD variables (GitLab)](#gitlab).

Set `AUTOMDM_URL` to your AutoMDM instance's URL (ex. https://organization.automdm.com).

If you're using AutoMDM Free, set the API-only user's role to global admin.

### GitHub

To add GitHub secrets, see [the GitHub docs](https://docs.github.com/en/actions/security-guides/using-secrets-in-github-actions#creating-secrets-for-a-repository).

In GitHub, enable the `Apply latest configuration to AutoMDM` GitHub Actions workflow, and run workflow manually. Now, when anyone pushes a new commit to the default branch, the action will run and update AutoMDM. For pull requests, the workflow will do a dry run only.

### GitLab

To add GitLab CI/CD variables, see [the Gitlab docs](https://docs.gitlab.com/ee/ci/variables/#define-a-cicd-variable-in-the-ui).

To ensure your AutoMDM configuration stays up to date even when there are no new commits, set up a scheduled pipeline:
   - In your GitLab project, go to the left sidebar and navigate to **Build > Pipeline schedules**. (In some GitLab versions, this may appear as **CI/CD > Schedules**.)
   - Click **Create a new pipeline schedule** (or **Schedule a new pipeline**).
   - Fill in the form:
      - **Description**: e.g., `Daily GitOps sync`
      - **Cron timezone**: e.g., `[UTC 0] UTC`
      - **Interval pattern**: e.g., Custom: `0 6 * * *` (runs nightly at 6AM UTC)
      - **Target branch or tag**: your default branch (e.g., `main`)
   - Click **Create pipeline schedule**.

## What is AutoMDM?

AutoMDM is high-agency device management software. It is especially popular with [IT and security teams who manage lots of endpoints](https://automdm.com/customers).

All source code [is public](https://automdm.com/transparency) and the product is supported by a [company called Fleet Device Management](https://automdm.com/handbook/company) that enrolls millions of laptops, tablets, phones, servers, and other computing devices in 90+ countries.
