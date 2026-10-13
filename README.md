# Buggies

**Catches the bug before it ships — and proves it did after.**

Buggies reviews a diff, hunts for real defects in the code the change can reach,
and only reports a finding once it has *reproduced* the bug in a sandbox. No
reproduction, no report. It is a thin, opinionated shell over the shared
[`kernel`](../README.md) engine, tuned to be **quiet**: high precision, low noise,
predictable per-seat cost.

Start with [VISION.md](VISION.md), then [ARCHITECTURE.md](ARCHITECTURE.md),
[STACK.md](STACK.md), [USAGE.md](USAGE.md), [OPERATING.md](OPERATING.md),
[ROADMAP.md](ROADMAP.md).

## How it works

```
diff ──► scope (blast radius)     which code can this change actually affect?
     ──► scaffold (eslint, tsc)   cheap deterministic pre-filters
     ──► triad                    propose ─► attack ─► prove
     ──► verify (sandbox)         reproduce the bug on real code, twice
     ──► store                    stable ID, encrypted, minimal retention
     ──► post                     PR review comment + CI check run
```

A finding that can't be reproduced by the sandbox is never stored; a dismissed
finding stays quiet on re-run; the same bug always gets the same stable ID. The
CI check fails **only** when a reproduced finding is data-loss or exploitable —
clean changes get a green "no findings" check, never noise.

The model cascade escalates only when it pays off: a cheap pass filters
candidates, a mid pass confirms, a frontier pass proves the survivors
(**Haiku → Sonnet → Opus** aliases). By default the models run through on-device
Claude (the `claude` CLI) — no `ANTHROPIC_API_KEY` and no API billing; set
`BUGGIES_MODEL_BACKEND=anthropic` to use the API instead.

## Two ways to run

**One-shot** — review a single diff by its base/head commits (`buggies review`).

**Unattended** — `buggies serve` runs as a [GitHub App](OPERATING.md): a
`pull_request` webhook (HMAC-verified) lands in a SQLite queue (idempotent on
`repo@head_sha`), a worker mints an installation token, checks out the exact
head, reviews it, and posts a check run plus a review comment. Pull requests
from forks are treated as untrusted — their code runs with secret-bearing env
stripped, and the model never sees repository secrets.

## Layout

| path | owns |
|------|------|
| `cmd/buggies` | CLI: `review`, `index`, `radius`, `serve`, `mcp`, `version` |
| `internal/pipeline` | the left door: scope → triad → verify → store |
| `internal/unattended` | the serve worker: queue → review → post, with skip handling |
| `internal/queue` | SQLite job queue, idempotency, retries |
| `internal/integrations/github` | GitHub App: webhook, installation tokens, review + check-run posters |
| `internal/agents` | proposer / skeptic / prover — conservative, stop-early |
| `internal/claudecli` | on-device Claude backend (shells to the `claude` CLI) |
| `internal/policy` | model cascade policy, budgets, self-throttle thresholds |
| `internal/budget` | per-install spend cap |
| `internal/security` | fork isolation (secret-stripping env for untrusted code) |
| `internal/store` | Buggies' own finding store (encrypted, minimal retention) |
| `internal/scaffold` | deterministic pre-filters (eslint, tsc) |
| `internal/surfaces/{prcomment,checkrun}` | renders the PR comment and CI check |
| `deploy` | self-hosted compose + WSL sandbox setup |
| `testdata` | seeded-bug fixture repos — the start of the eval corpus |

## Quickstart

Requires Go 1.26+, Docker + **gVisor (`runsc`)** in WSL2 Ubuntu, and the `claude`
CLI on PATH (or `BUGGIES_MODEL_BACKEND=anthropic` with `ANTHROPIC_API_KEY`).
Sandbox images are prebuilt; build them once from the kernel repo:

```sh
# one-time: build the sandbox image (from the kernel repo root)
docker build -t buggies/sandbox-node:22 ../sandbox/images/node

# one-shot: review a diff — reproduces any finding before reporting it
BUGGIES_CLAUDE_BIN=claude ./bin/buggies review \
  --repo /path/to/target --name owner/repo \
  --base <base-sha> --head <head-sha>
```

Buggies **refuses to run untrusted code on runc**; install gVisor so the sandbox
runs under `runsc`. On a dev machine without it, `BUGGIES_ALLOW_RUNC=1` opts out
(never in production).

Run unattended:

```sh
BUGGIES_WEBHOOK_SECRET=…  BUGGIES_GITHUB_APP_ID=…  BUGGIES_GITHUB_APP_KEY=/path/key.pem \
BUGGIES_CLAUDE_BIN=claude  ./bin/buggies serve --addr :8080
```

See [OPERATING.md](OPERATING.md) for the full env (`BUGGIES_BUDGET_CENTS`,
`BUGGIES_GITHUB_HOST`, `BUGGIES_SKILLS`, …), the webhook wiring, and a sample
systemd unit.

## Status

**v1.0.0 — unattended review is live.** M1–M12 complete: scaffold, gVisor
sandbox (no network, read-only rootfs, resource caps, registry-only egress
proxy), the tree-sitter + SQLite code graph, the triad (conservative agents,
cascade proof, encrypted store, stable IDs), and the `serve` GitHub App
(webhook → queue → installation checkout → review → check run + comment),
proven end-to-end against a live pull request under gVisor.

## Local development

Two modules, one workspace. `go.work` (git-ignored) is just `use (. ..)` — the
`kernel` repo lives one directory up, so uncommitted kernel changes are picked up
during co-development. `go.mod` pins the kernel by tag (**`v1.31.0`**) and keeps a
`replace => ..`, so `GOWORK=off go build` resolves it from the working tree; drop
the replace (and set `GOPRIVATE=github.com/joshblazer`) to build against the
pinned tag from the private kernel repo. The sandbox runs in WSL2 Ubuntu
(Docker + gVisor); see [`deploy/wsl-setup.md`](deploy/wsl-setup.md).

## License

Private. All rights reserved.
