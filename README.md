# Buggies

**Catches the bug before it ships — and proves it did after.**

Buggies reviews a diff, hunts for real defects in the code the change can reach,
and only reports a finding once it has *reproduced* the bug in a sandbox. No
reproduction, no report. It is a thin, opinionated shell over the shared
[`kernel`](../README.md) engine, tuned to be **quiet**: high precision, low noise,
predictable per-seat cost.

Start with [VISION.md](VISION.md), then [ARCHITECTURE.md](ARCHITECTURE.md),
[STACK.md](STACK.md), [USAGE.md](USAGE.md), [ROADMAP.md](ROADMAP.md).

## How it works

```
diff ──► scope (blast radius)     which code can this change actually affect?
     ──► scaffold (eslint, tsc)   cheap deterministic pre-filters
     ──► triad                    propose ─► attack ─► prove
     ──► verify (sandbox)         reproduce the bug on real code, twice
     ──► store                    stable ID, encrypted, minimal retention
     ──► PR comment               only for reproduced, high-confidence findings
```

The model cascade escalates only when it pays off: **Haiku** filters candidates →
**Sonnet** confirms → **Opus** proves the survivors. A finding that can't be
reproduced by the sandbox is never stored; a dismissed finding stays quiet on
re-run; the same bug always gets the same stable ID.

## Layout

| path | owns |
|------|------|
| `cmd/buggies` | CLI: `review`, `index`, `radius` |
| `internal/pipeline` | the left door: scope → triad → verify → store |
| `internal/agents` | proposer / skeptic / prover — conservative, stop-early |
| `internal/policy` | model cascade policy, budgets, self-throttle thresholds |
| `internal/store` | Buggies' own finding store (encrypted, minimal retention) |
| `internal/scaffold` | deterministic pre-filters (eslint, tsc) |
| `internal/surfaces/prcomment` | renders the PR comment |
| `internal/integrations/github` | PAT poster now, GitHub App next |
| `deploy` | self-hosted compose + WSL sandbox setup |
| `testdata` | seeded-bug fixture repos — the start of the eval corpus |

## Quickstart

Requires Go 1.26+, Docker (WSL2 Ubuntu), and `ANTHROPIC_API_KEY`. Build the
sandbox image once, then review a diff by its base/head commits:

```sh
# one-time: build the sandbox image (from the kernel repo root)
docker build -t buggies/sandbox-node:22 ../sandbox/images/node

# review a diff — reproduces any finding before reporting it
BUGGIES_DATA=~/.buggies ./bin/buggies review \
  --repo /path/to/target --name owner/repo \
  --base <base-sha> --head <head-sha>
```

Add `BUGGIES_ALLOW_RUNC=1` for dev machines without gVisor installed. Set
`GITHUB_TOKEN` (repo scope) to post the finding as a PR comment.

## Status

- **M1–M4 complete:** scaffold, sandbox (8 integration tests on real Docker: no
  network, read-only rootfs, resource caps, registry-only egress proxy), code
  graph (tree-sitter JS/TS + SQLite blast radius), and the triad (conservative
  agents, Sonnet→Opus proof cascade, encrypted store, stable IDs).
- **M5 in progress:** the first live end-to-end run against a seeded bug in a real
  public repo, becoming the first entry in the eval corpus.

## Local development

Two modules, one workspace. `go.work` (git-ignored) points at `..` — the `kernel`
repo, which lives one directory up — so uncommitted kernel changes are picked up
during co-development. CI resolves the kernel by its pinned tag instead. The
sandbox runs in WSL2 Ubuntu (Docker + gVisor); see [`deploy/wsl-setup.md`](deploy/wsl-setup.md).

## License

Private. All rights reserved.
