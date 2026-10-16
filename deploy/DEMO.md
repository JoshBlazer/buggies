# Public demo — let people see Buggies work on GitHub

The goal of this runbook is a **persistent, zero-steps-for-the-viewer demo**:
someone clicks a GitHub PR and sees the `buggies` check run and inline review
comments — the real product surface — without installing, cloning, or running
anything themselves.

It has two parts:

1. **Host the daemon** once so GitHub can deliver webhooks to it. This is the
   normal production deploy — see [OPERATING.md §9](../OPERATING.md). Nothing
   here replaces it.
2. **Seed public demo PRs** with known bugs so every visitor sees the same,
   deliberately interesting reviews. That is what this file adds.

> This is the live product, not a mockup. For a mockup that needs no running
> daemon (a GIF/screenshots embedded in the README), see "Recording a visual"
> at the bottom.

## What the demo shows

The seed script turns the three eval fixtures (`testdata/fixtures/`) into three
public repos, each with one open PR. Together they tell the whole story —
Buggies catches real bugs, grades severity, and does **not** cry wolf:

| Demo repo (`buggies-demo-…`) | The PR does | Expected review |
|---|---|---|
| `…-gold-tier-discount` | Capitalizes `'gold'`→`'Gold'`, so gold-tier customers silently lose their 20% discount | **Blocking** correctness / data-loss finding, reproduced |
| `…-page-drops-last` | Changes a pagination slice end so every page silently drops its last record | Correctness finding (dropped records), reproduced |
| `…-clean-refactor` | Rewrites a `for`-loop as `reduce`, no behaviour change | **No finding** — a clean change passes untouched |

The clean one matters as much as the bugs: it's the evidence the reviewer is
calibrated, not noisy.

## Prerequisites

- A deployed, publicly reachable daemon with the gVisor sandbox healthy — the
  whole of [OPERATING.md §9](../OPERATING.md). A review with an unhealthy
  sandbox posts a neutral "sandbox unavailable" check, not a reproduction, so
  verify step (d)(2) there before seeding.
- `gh` authenticated (`gh auth status`) as a user who can create repos under
  the demo owner.
- The Buggies GitHub App (see OPERATING.md §2–3).

## 1. Create and seed the demo repos

Pick a dedicated owner (a throwaway org is cleanest — the repos are public):

```sh
# from the buggies repo root
OWNER=<your-demo-org-or-user> ./deploy/seed-demo.sh
```

This creates `OWNER/buggies-demo-gold-tier-discount`,
`…-page-drops-last`, and `…-clean-refactor`, pushes each fixture's `base/` tree
to `main`, pushes its `head/` tree to a `proposed-change` branch, and opens a
PR. It is **idempotent** — re-run it any time to reset the demo to a known
state (handy after you've been poking at the PRs).

Seed a subset by naming fixtures: `OWNER=… ./deploy/seed-demo.sh gold-tier-discount`.

## 2. Install the App on the demo repos

The PRs exist now, but no review runs until the App is installed. Either:

- install it on the three repos individually (App → Install → select repos), or
- install it **org-wide** on the demo owner, so the seed script's future repos
  are covered automatically.

Installation emits webhooks for the already-open PRs, so reviews start within a
minute. (If you installed *before* seeding, the `pull_request` events on open
already triggered them.)

## 3. What viewers see (and the links to share)

On each PR, with no account or setup, a visitor sees:

- the **`buggies` check run** on the PR's Checks tab — green for the clean
  change, failing/neutral with a summary for the bugs;
- the **review comment(s)** with the one-line claim, severity, and the
  **reproduction proof** (the sandbox transcript that shows the bug actually
  happening — the thing that separates this from a linter).

Share the PR URLs directly, e.g.
`https://github.com/OWNER/buggies-demo-gold-tier-discount/pull/1`. A short
`README` on the demo org linking all three makes a one-click tour.

## 4. Keeping it live

- The daemon must stay up and the gVisor sandbox healthy; `GET /healthz` and
  `GET /readyz` (OPERATING.md §9d) are the probes to alarm on.
- Re-run `seed-demo.sh` to refresh after someone closes/merges a PR.
- The per-install **budget cap** (`internal/budget`) still applies to the demo
  installation — set it high enough that re-seeding doesn't exhaust it, or the
  reviews will skip with a budget note.

## Recording a visual (no running daemon needed by the viewer)

To put a visual in the `README` instead of (or alongside) the live demo, capture
the dashboard and/or a real PR review once and embed it — GitHub renders PNGs,
JPGs and GIFs inline. The top-level [README](../README.md) already embeds
`docs/dashboard-overview.jpg` (the dashboard) and the real PR-comment body; see
[`docs/README.md`](../docs/README.md) for how those were captured and how to
regenerate them with [`capture-dashboard.sh`](capture-dashboard.sh). This is the
lowest-friction way for people to "see it work" — no running daemon needed by
the viewer.
