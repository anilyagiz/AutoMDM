---
name: automdm-gitops-export
description: Export a live AutoMDM instance into a ready-to-use GitOps repository laid out the way `automdmctl new` scaffolds it (default.yml, teams/*.yml, labels/, platforms/[os]/[kind]/), with secrets replaced by environment variables and a read-only `automdmctl gitops --dry-run` to prove the repo matches AutoMDM. Use when someone wants to migrate AutoMDM settings configured in the UI or API to GitOps, export AutoMDM to a GitOps repo, turn UI settings into YAML, adopt GitOps on an existing AutoMDM, or replace `automdmctl generate-gitops`, even if they never say "export". It builds a new repo; to edit or validate an existing GitOps repo, use the automdm-gitops skill instead.
compatibility: Needs a shell, Python 3.9+, automdmctl 4.84.0+ logged in to AutoMDM as a global admin, and network access to the AutoMDM server.
---

# Export AutoMDM to GitOps

This is the cross-agent entry point (Codex, Cursor, GitHub Copilot, Gemini CLI, Kilo Code, and other agents that read `.agents/skills/`) for the `automdm-gitops-export` skill. The skill lives at `.claude/skills/automdm-gitops-export/` in the root of this repository, with its `scripts/` and `references/` folders, so every agent follows the same instructions.

Read `.claude/skills/automdm-gitops-export/SKILL.md` now and follow it exactly as if it were this file. Its relative references (`scripts/...`, `references/...`) resolve from that folder.
