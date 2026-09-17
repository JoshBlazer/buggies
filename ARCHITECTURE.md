# ARCHITECTURE.md — Buggies

## The shape in one picture

Buggies has **two entry doors into one pipeline.** Both doors feed the same kernel machinery (code graph, sandbox, triad, finding store) and produce the same object — a finding with proof attached.

```
                          ┌─────────────────────────────────────────┐
  LEFT DOOR (build-time)  │                                          │
  PR / commit / save  ───▶│                                          │
                          │   1. SCOPE      (code graph: blast       │
                          │                  radius of the change)   │
                          │   2. TRIAD      (propose → attack →      │
  RIGHT DOOR (runtime)    │                  prove, via kernel)      │──▶ FINDING
  prod error signal  ────▶│   3. VERIFY     (sandbox: run it,        │    (+ proof)
  (read-only telemetry)   │                  units-first)            │
                          │   4. STORE      (finding store: dedup,   │
                          │                  stable ID, lifecycle)   │
                          │   5. CALIBRATE  (prod feedback tunes     │
                          │                  future noise)           │
                          └─────────────────────────────────────────┘
                                              │
                          ┌───────────────────┴───────────────────┐
                          ▼           ▼            ▼               ▼
                    PR comment   Incident     CI gate        Dashboard
                                   card
```

The two doors are ~15% of the engineering. The pipeline kernel is the other 85%. That is why Buggies is one product and not two.

## The left door — build-time (A)

Triggered by developer activity, escalating in cost with the significance of the event:

| Trigger | What runs | Cost tier |
|---|---|---|
| **On save/keystroke** | Cheap local model, obvious issues only | Minimal |
| **On commit** | Changed files + their blast radius from the graph | Low |
| **On PR** | Full adversarial triad + sandbox reproduction | **The money moment** |
| **On merge to main** | Deep async hunt, security-focused (v2), runs minutes not seconds | High, async |

The PR is the moment that matters. Everything before it is a cheap preview; everything after is deep background work.

**Flow:** diff arrives → graph computes blast radius (what could this change break, including callers three files away) → triad proposes candidate findings within that scope → sandbox attempts to reproduce each with a generated test → only reproduced findings are stored → surfaced as PR comments.

## The right door — runtime (B)

Triggered by a signal from the team's **existing** error tracker or observability stack. Buggies is a **read-only consumer of telemetry, never a producer.**

**Critical architectural decision: do not build an APM.** Integrate read-only with Sentry, Datadog, Bugsnag, CloudWatch, OpenTelemetry collectors. Buggies wants error signatures, stack traces, deploy markers, and route-level traffic. It must never be in the hot path of a request and never be responsible for uptime.

This decision buys: install drops from quarters to "paste an API key"; Buggies inherits the customer's existing PII scrubbing instead of owning it; Buggies complements Datadog rather than competing with it; infra cost stays sane.

**Flow:** a new error signature or latency regression arrives → graph maps it to the responsible source location → triad root-causes it → sandbox reproduces it locally (units-first) → stored as an incident finding with a candidate fix → surfaced as an incident card.

## The loop between the doors — the part nobody else has

The two doors are not parallel features; they feed each other. This is the v1 differentiator.

- **Told-you-so attribution.** When an incident (right door) maps to a source location that a prior PR finding (left door) already flagged and the team dismissed, the incident card says so. Highest-value screen in the product; earns the right to gate merges.
- **Sibling hunt.** An incident reveals a defect pattern → graph searches every structurally similar site across all repos → "this pattern exists in 7 other places, 3 on reachable paths." One incident becomes eight fixes.
- **Silence scoring / calibration.** Findings on hot code paths that never manifest in prod get auto-downgraded. Buggies gets quieter about categories it has been wrong about *in this codebase*, automatically. Per-customer calibration, not one global setting.
- **Deploy-aware risk.** The graph knows which paths carry real traffic (from telemetry). A bug in a cold admin script and the same bug in checkout get different levels. Every static tool guesses at this; Buggies knows.
- **Reverse test generation.** A prod incident becomes a permanent regression test, committed back via PR. A concrete artifact the team keeps — stickiness beyond the subscription.

## How the kernel plugs in

Buggies is a thin, opinionated shell over the kernel (see KERNEL.md). Buggies owns:

- **Its own finding-store instance** (its own database, its own data, behind the wall).
- **Its triad agents** — conservative, low-noise, stop-early. Buggies' personality is "don't cry wolf."
- **Its model-router policy** — optimized for predictable per-seat gross margin.
- **Its integrations** — git hosts (GitHub App first), error trackers, IDEs, CI.
- **Its surfaces** — PR comment, incident card, CI gate, dashboard (see USAGE.md).
- **Its calibration logic** — the prod-feedback loop is Buggies-specific and lives here, not in the kernel.

Buggies pins a kernel version and upgrades on its own schedule.

## Determinism — non-negotiable for the gate

An LLM gives different findings on the same code across runs. That is fine for advice and disqualifying for a merge gate. Buggies achieves stable verdicts through the kernel's fingerprint-keyed caching and stable finding IDs: the same code produces the same finding. A PR that was green does not flip red on a re-run because the model felt different that afternoon.

## Trust, installation, and data boundaries

- **Installable = it reads all their source.** That triggers security review, SOC 2, data-residency questions, and for part of the market a hard requirement for **self-hosted / bring-your-own-key** deployment. Architect for a self-hostable deployment mode from day one — not as a v3 enterprise afterthought.
- **The finding store is a breach target.** A queue of unpatched, verified bugs in customer codebases is extremely attractive to attackers. This shapes storage (encryption, minimal retention), access control, and incident-response posture.
- **The sandbox runs customer code.** It is the single most dangerous component. It inherits the kernel's ephemeral, network-deny, resource-bounded design and Buggies adds no exceptions to that at build-time (unit runs need no network at all).

## v0 boundaries (locked)

- **Units-first sandbox.** Isolated units and generated tests only. No full-application stand-up. Full-app reproduction is a v1 kernel capability.
- **Broad language coverage from the start.** The sandbox's per-ecosystem adapter model (KERNEL.md) is what makes this feasible — each major language is an adapter, not a rewrite. v0 targets the major ecosystems (see STACK.md for the specific list and order).
- **Left door first, right door as a fast follow.** The PR reviewer must prove the precision claim before the loop can close. See ROADMAP.md.
