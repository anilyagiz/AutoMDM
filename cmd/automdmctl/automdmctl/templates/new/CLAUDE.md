@AGENTS.md

## Claude Code notes

- The repository's agent instructions live in `AGENTS.md`, imported above, so Claude Code and the other agents on the team follow the same rules.
- The `automdm-gitops` skill is registered under `.claude/skills/automdm-gitops/` and hands off to the full skill in `.agents/skills/automdm-gitops/`. Invoke it with `/automdm-gitops`, or let it trigger on its own for GitOps work.
