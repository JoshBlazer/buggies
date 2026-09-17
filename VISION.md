# VISION.md — Buggies

## One sentence

**Buggies catches the bug before it ships and proves it did after.**

## The problem

Every AI code reviewer on the market is stuck on the same tradeoff: catch more bugs and drown the team in false positives, or stay quiet and miss real ones. The leaders sit at different points on that frontier — one catches ~82% of seeded bugs in its own benchmark but fires ~11 false positives per run; another catches ~44% with far less noise. Independent re-evaluation routinely finds real-world detection well below the published numbers. The whole category is arguing about where to sit on a line nobody can escape.

Worse, they're all flying blind on their own accuracy. Their only feedback signal is "did the developer click dismiss," which is close to useless: developers dismiss real bugs because they're busy, and accept fake ones because the comment sounded confident. No reviewer on the market knows whether the bugs it flagged were real, because it never finds out what actually broke.

## The edge

Buggies refuses the tradeoff and closes the loop. Two things make it structurally different, and neither is "we prompt the model better":

### 1. Proof, not opinion

Every finding ships with a **reproduction** — a failing test, a stack trace, a diff that makes it pass. Competitors return "this might be a null deref on line 412." Buggies returns a test you can run that fails.

This takes Buggies off the precision/recall frontier entirely. An unreproducible finding never reaches the developer — it's discarded inside the system. The developer only ever sees findings that are **true by construction.** The false-positive war doesn't touch a product whose findings are self-verifying.

This is a *systems* capability, not a prompting one. A raw model reasons about code without running it — it guesses, confidently and often wrongly, and cannot tell you which of its own claims are true. Buggies runs the guess in a sandbox and keeps only what survives.

### 2. Ground truth from production

This is the half no PR-only competitor can copy. Buggies reads the team's existing error tracker — read-only, one API key, no production install — and learns from what **actually broke**. Production is the only honest label: a crash is a confirmed positive; a finding on a path that has run ten million times without incident is a probable false positive.

Six months in, Buggies is calibrated to *that specific codebase* — quiet about the classes of bug that never manifest there, loud about the ones that have actually bitten. A competitor starting fresh cannot catch up, because they don't have the second half of the loop. A chat window starts from zero every single time.

## Why not just use the LLM directly

The LLM is the guesser. Buggies is the machine around the guess: it runs the guess in a sandbox, checks it against the whole codebase via a persistent cross-repo graph, weighs it against this team's real production history, dedupes it, gives a deterministic verdict a merge gate can trust, and runs unattended on every PR at 3am. None of that is the model. All of it is why someone pays for Buggies instead of opening a chat tab.

The internal test for any feature: *could the customer get this by pasting into a chat window?* If yes, it's table stakes, not our value. If no — because it needed execution, persistent cross-repo state, their private production history, or deterministic unattended operation — that's the moat.

## The four risk types

| Type | What it is | How it's proven |
|---|---|---|
| **Correctness** | Logic errors, off-by-one, null handling, race conditions | Generated failing test |
| **Contract** | Breaking changes to callers, API/schema drift, silent behavior change | Cross-repo caller analysis + observable behavior diff |
| **Resilience** | Unhandled failure, resource leaks, missing timeouts, retry storms | Fault injection in sandbox |
| **Security** | Injection, IDOR, auth bypass, secrets, unsafe deserialization | Sandboxed exploit PoC (v2) |

Contract is deliberately prioritized early — it's the class of bug that costs the most and that no diff-only reviewer sees, and it's where the cross-repo graph earns its keep.

## Levels: a two-axis model

Do not invent a custom severity scale. Rank on two axes:

- **Confidence:** unverified → statically proven → reproduced
- **Impact:** cosmetic → degraded → data-loss → exploitable

Only **reproduced × (data-loss or exploitable)** is a blocking, merge-gating alert. Everything else is advisory. This gives teams a policy dial they can tune, which is what gets a tool past the CI-gating decision.

**The ranking rule across every surface:** verification status first, severity second. A reproduced medium outranks an unverified critical. The entire brand is that Buggies does not cry wolf.

## Who buys it

Engineering leads at teams **under ~200 developers**, where one person owns both code quality and reliability. One budget line, one decision-maker. Above that size, review and observability are separate orgs with separate budgets — don't chase them until the mid-market motion works, and when you do, sell it as a code-quality tool that happens to read telemetry, not as an observability product.

There is also a large underserved segment below the well-funded Western enterprise: small teams and agencies with zero security coverage for whom $30/seat/month is real money. They start on the PR reviewer and grow into the production loop.

## Why they buy, why they stay, why it wins

- **Buy:** ROI is provable *before payment.* Backfill their last year of incidents against their repo and show "61 of your 140 production incidents were catchable at PR time." That artifact costs one read-only API key.
- **Stay:** the tool gets more accurate the longer it runs on their code. The switching cost is the accumulated per-codebase calibration, which resets to zero if they leave. Real lock-in that isn't hostile.
- **Win:** the category is stuck on catch-rate vs noise. Buggies escapes the tradeoff by only shipping reproducible findings, then compounds an accuracy lead competitors structurally can't match.

## Business model

- **Per seat**, ~$20–40/dev/month — what the market expects, easy to buy.
- **The production loop is a tier, not a meter.** Metering incidents punishes customers exactly when they're having a bad week — which is when they most need the tool. Charge more for the tier and absorb the variance.
- **Free forever for public repos** — standard growth motion in this category, and it doubles as an evaluation corpus.
- Unit economics depend on the model cascade (see STACK): cheap model filters, frontier model runs only on survivors. Get this wrong and gross margin is negative at scale.

## The flywheel

Every dismissal, every accepted fix, and above all every "this was flagged and shipped anyway and then broke in prod" is training and evaluation signal. A corpus of *verified true and false positives from real repos* is an asset nobody can buy. Instrument for it from the first commit.

## Non-goals

- Buggies is **not an APM.** It consumes telemetry read-only; it never sits in anyone's request path and is never responsible for uptime.
- Buggies does **not** ship unreproducible findings to developers as if they were real.
- Buggies does **not** chase enterprise co-selling before mid-market works.
- Buggies shares **no runtime, data, or credentials** with BugOps. It shares only the kernel library. (See KERNEL.md.)
