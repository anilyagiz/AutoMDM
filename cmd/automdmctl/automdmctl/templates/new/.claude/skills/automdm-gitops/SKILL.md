---
name: automdm-gitops
description: Make changes to an AutoMDM GitOps repository (default.yml, teams/*.yml or teams/*.yml, and the policies, reports, labels, scripts, software, configuration profiles, and DDM declarations they reference). Use this whenever a task touches AutoMDM-managed devices through YAML, even if the user never says "GitOps" - adding, scoping, or updating software (AutoMDM-maintained apps, custom packages, App Store or Google Play apps), writing a policy or report, remediating a CVE, creating or editing a .mobileconfig, DDM .json, Windows CSP .xml, or Android policy .json, changing labels, controls, OS update deadlines, or org settings, or reviewing a GitOps pull request. Covers the layout rules automdmctl enforces, validation against upstream schemas (osquery tables, Apple, Microsoft, Google), and automdmctl dry runs.
allowed-tools: Read, Grep, Glob, Edit, Write, WebFetch, WebSearch, Bash(python3:*), Bash(plutil:*), Bash(xmllint:*), Bash(jq:*), Bash(contour:*), Bash(git diff:*), Bash(git status:*), Bash(git log:*), Bash(ls:*), Bash(find:*)
effort: high
---

# AutoMDM GitOps

Task: $ARGUMENTS

This is the Claude Code entry point for the `automdm-gitops` skill. The skill itself lives at `.agents/skills/automdm-gitops/` in the root of this repository, the cross-agent location that Codex, Cursor, GitHub Copilot, Gemini CLI, and Kilo Code also read, with its `references/` files and the `scripts/validate.py` checker beside it. Keeping one copy there means every agent follows the same instructions.

Read `.agents/skills/automdm-gitops/SKILL.md` now and follow it exactly as if it were this file. Its relative references (`references/...`, `scripts/validate.py`) resolve from that folder.
