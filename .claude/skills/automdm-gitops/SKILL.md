---
name: automdm-gitops
description: Make changes to an AutoMDM GitOps repository (default.yml, teams/*.yml or teams/*.yml, and the policies, reports, labels, scripts, software, configuration profiles, and DDM declarations they reference). Use this whenever a task touches AutoMDM-managed devices through YAML, even if the user never says "GitOps" - adding, scoping, or updating software (AutoMDM-maintained apps, custom packages, App Store or Google Play apps), writing a policy or report, remediating a CVE, creating or editing a .mobileconfig, DDM .json, Windows CSP .xml, or Android policy .json, changing labels, controls, OS update deadlines, or org settings, or reviewing a GitOps pull request. Covers the layout rules automdmctl enforces, validation against upstream schemas (osquery tables, Apple, Microsoft, Google), and automdmctl dry runs.
allowed-tools: Read, Grep, Glob, Edit, Write, WebFetch, WebSearch, Bash(python3:*), Bash(plutil:*), Bash(xmllint:*), Bash(jq:*), Bash(contour:*), Bash(git diff:*), Bash(git status:*), Bash(git log:*), Bash(ls:*), Bash(find:*)
effort: high
---

# AutoMDM GitOps

Task: $ARGUMENTS

The `automdm-gitops` skill lives at `cmd/automdmctl/automdmctl/templates/new/.agents/skills/automdm-gitops/`, the copy `automdmctl new` scaffolds into customer repositories. Keeping one copy there means this repository and customers' repositories follow the same instructions.

Read `cmd/automdmctl/automdmctl/templates/new/.agents/skills/automdm-gitops/SKILL.md` now and follow it exactly as if it were this file. Its relative references (`references/...`, `scripts/validate.py`) resolve from that folder.
