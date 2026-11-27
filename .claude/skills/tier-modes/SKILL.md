---
name: tier-modes
description: Authoring guide for AutoMDM Free (!isPremiumTier) and Primo mode (isPrimoMode) gating in the AutoMDM frontend. Use when adding a new top-level page, feature page, or significant UI surface (modal, side panel, dashboard, settings section, new tab) where the Free / Primo treatment isn't already decided, OR when introducing NEW tier gating to code that doesn't have it yet. Do NOT load for edits inside already-gated code — the tier decision is already made there.
allowed-tools: Read, Grep, Glob, Bash(yarn test*)
effort: medium
---

# Tier modes (AutoMDM Free + Primo mode)

The canonical guide lives in **`frontend/docs/patterns.md` § Tier modes** — read it first. It covers what each mode is, how it's plumbed, the gating patterns, testing conventions, and the gotchas.

This skill exists to ensure that guide is followed and to catch the common gap: developers (and Claude) shipping a change that touches gated code without thinking through both modes.

## Before adding or modifying a gate

1. **Read `frontend/docs/patterns.md` § Tier modes** end to end. The critical asymmetry (`isPremiumTier` lives in AppContext, `isPrimoMode` does not) is the #1 source of bugs — internalize it before touching code.
2. **Identify which mode(s) apply** to the change:
   - **AutoMDM Free** — a premium feature being added or modified. When `!isPremiumTier`, show `<PremiumFeatureMessage />` or hide the feature entirely.
   - **Primo mode** — Primo is a Premium tenant with a single team. Any new multi-team UI (team switcher, "All teams," team creation, team-scoped table columns) needs to consider what a Primo user sees in its place — usually a collapsed single-team view.
   - **Both** — most common for premium features that involve team selection or multi-team affordances. Use a dual gate like `isPremiumTier && !isPrimoMode`.
3. **Grep canonical examples** before inventing a pattern:
   - `frontend/components/CommandPalette/` — the most thorough reference; handles both flags via spread-based item arrays, dual gates, and named test suites
   - `frontend/pages/admin/ManageTeamsPage/` — the canonical Primo disabled-button-with-tooltip pattern
   - `frontend/components/PremiumFeatureMessage/` — the canonical full-page paywall component

## When implementing

- **Mirror the destination's gate exactly** when adding a nav item, palette entry, or link that points at a gated page. If the destination renders `<PremiumFeatureMessage />` on Free, gate the entry on `isPremiumTier`.
- **Reuse existing flags** before adding new ones. `ICommandPaletteContext` and `AppContext` already expose most checks (`canAddSoftware`, `canManageReportAutomations`, etc.). Add a new flag only when no existing one matches the destination's predicate.
- **Don't invent paywall UI.** Use `<PremiumFeatureMessage />` for the standard premium-feature page-or-card paywall.
- **Don't store `isPrimoMode` in component state.** Derive it from `config?.partnerships?.enable_primo` at the call site (see the gotcha in patterns.md).

## After implementing — required checks

1. **Test both modes.** For each new gated path, add at least one assertion:
   - Free: `isPremiumTier: false` context — the feature is hidden or replaced with the paywall.
   - Primo: `isPrimoMode: true` (via `config.partnerships.enable_primo`) — multi-team affordances collapse correctly.
   - See `CommandPalette/helpers.tests.ts` for the canonical structure (top-level `describe` blocks per mode).
2. **Run the related tests.**
   ```
   yarn test <path-to-your-tests>
   ```

## End-of-task gap check (required)

Before reporting the task done, check:

- **Did the change introduce a new feature surface?** A new top-level page, feature page, modal, side panel, dashboard, settings section, or tab — somewhere a user lands or interacts where the Free / Primo treatment is a real, open question.
- **Did the user's original request explicitly address what AutoMDM Free or Primo users see on that surface?**

If yes to the first AND no to the second, ask the user before declaring done:

> This added a new feature surface. Should we verify what AutoMDM Free and Primo users see?

Do **not** ask when:
- The change only edited inside already-gated code (added a field to a premium-only form, fixed a bug in a paywalled flow, etc.) — the tier decision is already made.
- The user explicitly addressed the gate ("make this premium-only," "leave Primo alone," etc.).

The goal is to catch unstated tier assumptions on genuinely new surfaces, not to nag on every gated-code edit.

## Common gotchas

See `frontend/docs/patterns.md` § Tier modes → Gotchas — the most important is that `isPrimoMode` is not in `AppContext` and gets silently lost when code moves between components.
