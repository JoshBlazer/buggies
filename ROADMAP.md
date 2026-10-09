# ROADMAP.md — Buggies

The phases of production, and — more importantly — what each phase *proves*. The purpose of this file is to stop the most common failure mode: building the fun parts first and never shipping the boring foundation that the whole thesis depends on.

## The one rule that governs sequencing

**The sandbox reproduction must exist in v0.** It is not a feature to add later — it is the entire reason Buggies is not a thin LLM wrapper. If you ship a "PR reviewer" that returns opinions before it can return proofs, then for that whole period you *are* a wrapper, and a savvy customer will say so. Proof-in-a-sandbox is v0 or the product has no edge.

## v0 — the precision claim

**Ships:** PR reviewer (left door). Correctness + Contract types. Every finding carries a reproduction (failing test) from the units-first sandbox. First two language adapters (JS/TS, Python). GitHub App + CLI.

**Proves:** that findings are true by construction and the false-positive war doesn't apply. Nothing else matters until this lands. If v0 doesn't feel obviously more trustworthy than a chat window, stop and fix that before building anything else.

**Explicitly deferred:** production loop, security type, full-app sandbox, additional languages, merge gating.

### v0 unattended delivery (M6–M12) — finishing the left door

The CLI `review` proves the precision claim (M5, passed). Unattended operation — review every PR on an installed repo with nobody at the keyboard — is the rest of v0's left door. All shell (Buggies integrations/surfaces/policy), built in dependency order, each with an exit check.

| M | Ships | Exit check |
|---|-------|-----------|
| **M6** | GitHub App webhook intake: receiver, HMAC signature verify, installation-token auth | `go test ./internal/integrations/github -run Webhook` — signed delivery → 200 + parsed PR job; tampered signature → 401 |
| **M7** | Durable SQLite job queue + idempotency (delivery-id + head-sha); comment update-in-place | integration test replays the same recorded webhook twice → asserts one review and one comment |
| **M8** | Check-run surface alongside the PR comment; CI-gate conclusion; update-in-place | post then re-run → one check run, updated not duplicated; conclusion = failure iff reproduced × (data-loss\|exploitable) |
| **M9** | Per-repo spend budget + "skipped for budget" PR note; self-throttle on the unattended path | drive a repo past budget → review skipped, note posted, throttle respected |
| **M10** | Untrusted-input hardening (all PR text treated as data) + fork-PR secret isolation | injection-fixture PR → findings identical with and without injected instructions; fork PR → no token in agent/sandbox env |
| **M11** | Agent skill library (one `SKILL.md` per bug class) with selective loading | corpus eval with skills on vs off → bugs caught, false alarms, cost per review for each |
| **M12** | MCP server (`buggies mcp`, stdio) + a Claude Code `SKILL.md` for pre-PR use | start the MCP server and call every tool (blast radius, review, get finding, dismiss) → valid response each |

**`buggies serve` (the M6–M12 capstone).** M6–M12 ship as libraries; `buggies serve` is the daemon that stands them up together: webhook (M6) → durable queue (M7) → worker → checkout of the PR head → review → review comment + CI check run (M8), gated by per-repo budget (M9) and running fork PRs with secrets stripped from the model subprocess (M10). The PR head is fetched via `refs/pull/<N>/head` from the base repo (`internal/checkout`), so a fork's clone URL and credentials are never needed; the head SHA is verified against the webhook's. Proven without GitHub: `go test ./internal/checkout` (head checked out at the expected SHA with the base reachable for diffing; moved-ref refused) and `go test ./internal/unattended -run ServeEndToEnd` (real HTTP server + background worker over loopback → same push delivered twice = one review, one post). The live GitHub leg (installation-token clone, posting) reuses the same `PostReview`/`UpsertCheckRun` the CLI `review --post` already exercises; it is wired, not yet run against a live installation.

## v0.5 — the read-only production connector

**Ships:** read-only error-tracker connector (Sentry first). Links existing prod incidents to source. Backfills historical attribution.

**Proves:** immediate value with **zero production install** — paste an API key, get "of your 140 incidents last year, 61 were catchable at PR time." This is the pre-payment sales artifact, and it's also your first stream of ground-truth labels. Cheap to build (read-only integration), disproportionately valuable.

**Why here and not later:** it costs one API key and it starts the ground-truth flywheel immediately. Every week it isn't running is a week of unlabeled data lost.

## v1 — the loop closes (the fundraise)

**Ships:** incidents trigger hunts (right door fully live). Sibling search. Silence scoring / per-codebase calibration. Told-you-so attribution. Reverse test generation. More language adapters (JVM, Go, Ruby). Full-application sandbox (the deferred v0 kernel capability) for richer runtime reproductions.

**Proves:** the thing nobody else has — a bug finder that learns from what actually breaks. This is the story that raises money and the moat that compounds. The gap between Buggies and any PR-only competitor becomes structural here, because they cannot replicate the loop.

## v2 — security + gating (expand the buyer)

**Ships:** Security risk type, using exploit-verification machinery hardened in BugOps and flowing across the kernel wall as code. Merge gating with a tunable confidence policy. Remaining language adapters (C#/.NET, PHP, Rust). Self-hosted mode fully productized for enterprise-adjacent buyers.

**Proves:** the buyer expands from eng manager to eng manager + security. The merge gate turns Buggies from advisory into part of the pipeline. Note the dependency: v2 security leans on what BugOps learns — this is the one designed synergy between the two products, delivered as shared kernel code, never shared data.

## What is deliberately NOT on this roadmap (yet)

These would be speculative fiction now and will be written when working code constrains them:

- Detailed data-model / schema docs — earn their place once the finding store is real.
- Formal API spec — once integrations are built, not before.
- Enterprise co-sell motion — not until mid-market works.
- Any full APM capability — permanently out of scope (see non-goals).

## The failure modes this roadmap defends against

1. **Shipping a wrapper.** Defended by: sandbox in v0, non-negotiable.
2. **Building the loop before the reviewer is trusted.** Defended by: v0 proves precision before v1 builds the loop.
3. **Boiling the ocean on languages.** Defended by: two adapters in v0, sequenced delivery after, each a bounded unit of work.
4. **Losing ground-truth data.** Defended by: read-only connector in v0.5, earliest feasible point.
5. **Negative gross margin.** Defended by: model cascade required before scale (see STACK).
