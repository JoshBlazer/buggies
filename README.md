# Buggies

Catches the bug before it ships and proves it did after.

Start with [VISION.md](VISION.md), then [ARCHITECTURE.md](ARCHITECTURE.md),
[STACK.md](STACK.md), [USAGE.md](USAGE.md), [ROADMAP.md](ROADMAP.md).
The shared engine is the `kernel` module (sibling repo); Buggies pins a
tagged version in `go.mod` and is a thin, opinionated shell over it.

## Layout

| path | owns |
|------|------|
| `cmd/buggies` | CLI: `review`, `index`, `serve` |
| `internal/pipeline` | the left door: scope → triad → verify → store |
| `internal/agents` | proposer / skeptic / prover — conservative, stop-early |
| `internal/policy` | model cascade policy, budgets, self-throttle thresholds |
| `internal/store` | Buggies' own finding store (encrypted, minimal retention) |
| `internal/scaffold` | deterministic pre-filters (eslint, tsc) |
| `internal/surfaces/prcomment` | renders the PR comment |
| `internal/integrations/github` | PAT poster now, GitHub App next |
| `deploy` | self-hosted compose |
| `testdata` | seeded-bug fixture repos — the start of the eval corpus |

## Local development

Two modules, one workspace. `go.work` (git-ignored) points at `../kernel` so
uncommitted kernel changes are picked up; CI resolves the kernel by tag.
The sandbox runs in WSL2 Ubuntu (Docker + gVisor) — see `deploy/wsl-setup.md`.
