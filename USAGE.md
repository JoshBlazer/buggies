# USAGE.md — Buggies

How Buggies installs, how it's invoked, and — the part that drives much of the architecture backwards — how it displays findings.

## Installation

The whole point of the design is that install is cheap:

- **Left door (PR reviewer):** install the GitHub App, one click. Review starts on the next PR. No config required to get value; config refines it.
- **Right door (production loop):** paste a read-only API key for the team's existing error tracker (Sentry first). No SDK, no code change, no prod deploy. On connect, Buggies immediately backfills historical incidents against the repo — the pre-payment ROI artifact.
- **Self-hosted mode:** the sandbox, graph, and finding store run in the customer's own cloud; optional model calls can point at the customer's own endpoint. First-class from day one.
- **Also available:** CLI, IDE extension, and an MCP server for use inside agentic coding tools.

## Modes of invocation

Event-driven, escalating with significance (see ARCHITECTURE.md for the trigger table). The developer does nothing to invoke it — it runs on save, commit, PR, and merge automatically, and on production signals as they arrive. Unattended operation at 3am is the point; a human never has to remember to ask.

---

## Findings display — the core principle

**Never make the developer leave their workflow to understand a finding, and never show a finding without its proof attached.**

**The universal ranking rule, on every surface: verification status first, severity second.** A reproduced medium outranks an unverified critical. The brand is that Buggies does not cry wolf, and the display enforces it.

---

## Surface 1 — the PR comment (primary)

Inline, on the exact line, where the developer already is. Structure:

- **One-line claim.** *"Null deref when `user.profile` is absent — reachable from the checkout route."*
- **Type + level badge.** `Correctness · Reproduced × Data-loss`
- **The proof, collapsed by default.** One click expands a **failing test** — real code that fails, not prose asking for trust. This single element is the entire differentiator. It is never omitted for a reproduced finding.
- **Suggested fix** as a committable diff.
- **Honest confidence line.** *"Reproduced in sandbox"* vs *"Static only — not yet reproduced."* An unreproduced finding is clearly marked as lower-tier and never presented as fact.

Design intent: a busy developer can trust-or-dismiss in seconds, and the proof is one click away when they want it.

## Surface 2 — the incident card (the right door; highest-value screen)

When something breaks in production, a card containing:

- **Mapped source location** — where in the code this incident originates.
- **Root cause** — the triad's explanation.
- **Local reproduction** — a failing test that recreates the prod failure locally.
- **Sibling instances** — "this same pattern exists in 7 other places, 3 on reachable paths."
- **Told-you-so line, when it applies** — *"Flagged on PR #4412, March 3, dismissed as a false positive."*
- **Candidate fix** as a diff, and the option to commit it as a permanent regression test.

This is the screen that earns renewals and the right to gate merges. Give it the most design care.

## Surface 3 — the CI gate (the binary output)

A status check that answers one question: **does this PR contain a reproduced, blocking-level finding — yes or no?** This is what makes Buggies part of the merge decision rather than advisory noise. Blocking level = `reproduced × (data-loss or exploitable)` by default; teams tune the policy dial (see VISION.md, levels).

## Surface 4 — the dashboard (for the buyer, not the coder)

Deliberately secondary. Exists mostly for whoever signs the check:

- **Caught vs shipped** trend over time.
- **Calibration state** — *"quiet on these 3 categories in your repo because they never manifest."* Makes the per-codebase learning visible.
- **Running ROI number** — incidents that were catchable at PR time.

Not the main event. The main event is the PR comment and the incident card, where the actual work happens.

---

## What the display deliberately does NOT do

- **Does not surface unreproducible findings as if real.** They're either discarded or clearly marked static-only and ranked below everything reproduced.
- **Does not spam re-runs.** Dedup + stable IDs (kernel finding store) mean the same bug is never reported twice as "new."
- **Does not get louder when ignored.** If a repo's dismissal rate climbs past threshold, Buggies gets *quieter on its own* — self-throttling is a feature. A tool developers learn to ignore is dead; the display defends against that outcome actively.

## The self-throttle rule (state it in code, not just docs)

If dismissal rate for a category in a given repo exceeds a threshold, downgrade or suppress that category for that repo automatically, and say so in the dashboard's calibration state. The tool would rather say less and stay trusted than say more and get muted.
