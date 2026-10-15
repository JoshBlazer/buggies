# Production readiness

Honest status of the gaps between "proven prototype" and "production grade."
Deployment how-to lives in [OPERATING.md](OPERATING.md) §9; this file tracks
*what is done* and *what remains*.

## Tier 1 — deployable self-host

| Item | Status | Where |
|------|--------|-------|
| Public HTTPS ingress (no smee) | **Done** | `deploy/docker-compose.yml`, `deploy/Caddyfile` (auto-TLS Caddy fronts `/webhook` + `/healthz` only) |
| Supervised gVisor sandbox daemon | **Done** | `deploy/docker-gvisor.service` (systemd supervises the side `dockerd`, `Restart=on-failure`) |
| CI builds against the kernel + eval gate | **Done** | `.github/workflows/ci.yml` (dual checkout mirroring the `replace => ..` layout; nightly/on-demand eval job) |
| Release artifact | **Done** | `Dockerfile` + `.dockerignore` (multi-stage; build context is the kernel root) |

> The CI `test` job needs a `KERNEL_REPO_TOKEN` secret (read access to the
> private kernel repo); the `eval` job needs `ANTHROPIC_API_KEY`.

## Tier 2 — production confidence

| Item | Status | Where |
|------|--------|-------|
| Retry + backoff + dead-letter | **Done** | `internal/queue`: a handler error retries with exponential backoff up to `DefaultMaxAttempts`, then dead-letters (Failed + `last_error`). Migration-safe. |
| Readiness probe | **Done** | `GET /readyz` reflects the real blocker (sandbox runtime reachable); 503 names the cause. `/healthz` stays liveness. |
| Metrics | **Done** | `GET /metrics` — Prometheus text: queue depth by state (incl. dead-letters) + process gauges. No client-lib dependency. |
| Over-large PR guard | **Done** | `pipeline.ErrDiffTooLarge` (cap `BUGGIES_MAX_CHANGED_FILES`, default 60) → neutral skip, before paying for scope/triad. |
| GitHub throttle resilience | **Done** | `internal/integrations/github` client retries 429 / secondary-403 / 5xx with `Retry-After` + backoff. |
| Headless model backend | **Covered by the SDK** | `BUGGIES_MODEL_BACKEND=anthropic` uses `anthropic-sdk-go`, which retries 429/5xx with backoff and applies a request timeout by default. |
| Quality gate / eval corpus | **Partial** | Harness + CI job exist; corpus is 3 fixtures and the eval job is reported, not blocking. Needs a larger labeled corpus before it can gate. |

## Tier 3 — multi-tenant / SaaS

| Item | Status | Notes |
|------|--------|-------|
| Data deletion on uninstall | **Done** | `installation`/`installation_repositories` removal purges the tenant's per-repo state dir and all its queue rows (`Reviewer.Uninstall`, `queue.PurgeRepo`). |
| Per-install spend cap | **Done (pre-existing)** | `internal/budget` caps monthly spend per installation. |
| Encryption at rest | **Done (pre-existing)** | Finding store is encrypted (`BUGGIES_STORE_KEY`); minimal retention. |
| Horizontal scale / HA | **Scoped, not built** | See below. |
| Per-tenant fairness (no noisy-neighbor) | **Scoped, not built** | Depends on the HA work below (multiple workers + a shared broker). The single serial worker + per-install budget is the current bound. |
| Compliance attestation (SOC2/DPA) | **Out of scope for code** | See below. |

## Deliberately not "finished" here (and why)

Two items cannot be *truthfully* marked done without a decision or an external
process, so they are called out rather than faked:

1. **Networked-DB HA (SQLite → Postgres, multiple workers).** The queue and
   finding store sit behind interfaces, and the queue is deliberately small and
   swappable — but a real HA deployment means standing up Postgres (or similar),
   porting the SQLite DDL/queries, adding `SELECT … FOR UPDATE SKIP LOCKED`
   claim semantics, and load-testing under concurrency. That needs an infra
   choice (managed Postgres? Redis?) and a running DB to test against; shipping
   an untested backend would be worse than naming the gap. The current
   single-node SQLite design is correct and sufficient for a single-tenant
   self-host.

2. **Compliance attestation (SOC2, DPA, etc.).** This is process and legal, not
   code. The *technical controls* it depends on are built — data deletion on
   uninstall, per-tenant encryption, minimal retention, loopback-only admin,
   fork isolation under gVisor — but the attestation itself is an organizational
   effort, not something this repo can complete.

## Also still open (calibration, not architecture)

- **Severity grader boundary.** `page-drops-last` (dropped records) is graded
  `degraded`, not `data-loss` — a genuine judgment call left un-overfit. Decide
  whether "records dropped from a page" is data-loss (tighten the rubric or the
  fixture) before hardening the eval into a blocking gate.
- **Dashboard assets in the container.** The image builds the binary only; mount
  `web/dist` (or embed it) if you enable the admin dashboard in the container.
- **Language coverage.** Node + Python sandboxes today; other ecosystems need
  their own adapter + image.
