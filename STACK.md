# STACK.md — Buggies

Technology choices with reasoning. The reasoning matters more than the choices — when you're tempted to undo one later, this file tells you what you were solving for.

## Guiding principles

1. **Buy or borrow the parts that aren't the moat.** Linters, SAST, type checkers, git host APIs, error-tracker APIs. Every hour spent rebuilding these is an hour not spent on the sandbox and the graph, which are the actual moat.
2. **The sandbox and the graph are the moat — invest there.**
3. **Determinism is a feature, engineer for it** — caching, fingerprints, stable IDs everywhere a model touches output.
4. **Self-hostable from day one** shapes every choice: prefer things that run in a customer's own cloud without a hard dependency on a service only you can host.

## Language coverage strategy (the market requirement)

Buggies needs broad language/framework verifiability to market well. The kernel's **per-ecosystem adapter** model is what makes this tractable: the sandbox core is language-agnostic; each ecosystem is an adapter that knows how to resolve deps, build, and run a test there.

**Order of adapter delivery** (by market footprint and adapter difficulty, easiest-high-value first):

1. **JavaScript / TypeScript** (Node) — largest footprint, simple test invocation.
2. **Python** — huge footprint, simple invocation, messy deps (handle with lockfile-aware resolution).
3. **Java / Kotlin (JVM)** — large enterprise footprint; Gradle/Maven build integration.
4. **Go** — clean toolchain, fast, easy adapter.
5. **Ruby** — meaningful footprint, straightforward.
6. **C# / .NET**, **PHP**, **Rust** — next wave.

Each adapter is a bounded unit of work. "Add a language" never means "rewrite the engine." Ship the first two before claiming breadth; sequence the rest publicly as a coverage roadmap (which itself is marketing).

## Core engine language

**Recommendation: a systems-capable language for the kernel and orchestration (Go or Rust), with per-ecosystem adapters shelling out to each language's native toolchain.**

- **Why not do everything in one high-level language:** you can't run Java tests well without the JVM toolchain, or Go tests without the Go toolchain. Adapters *must* use native toolchains regardless. So the core's job is orchestration, isolation, and speed — where Go (simple concurrency, fast, easy to deploy as a single binary customers can self-host) or Rust (max safety for the most dangerous component) shine.
- **Lean Go for v0** unless the team's deep expertise is Rust. Go's single-binary deploy story is a real advantage for self-hosting; its concurrency model fits the "many sandboxed runs at once" workload; it's faster to build in. Rust is the defensible choice specifically for the sandbox boundary if you want maximum safety there — a hybrid (Go orchestration, Rust sandbox core) is legitimate but costs a solo builder velocity. **Decide based on your own depth; the docs assume Go orchestration and note where Rust would pay off.**

## Sandbox executor

- **Isolation:** container-based ephemeral runners (gVisor or Firecracker microVMs for stronger isolation than plain containers, since we run untrusted customer code). Firecracker if you can afford the ops; gVisor as a lighter start.
- **Network:** default-deny at the sandbox boundary. Unit runs need no network.
- **Resource bounds:** hard CPU/mem/disk/wall-clock caps per run.
- **Per-ecosystem toolchain images:** one hardened base image per ecosystem with its toolchain baked in; dependency resolution is lockfile-aware to reproduce the customer's environment faithfully.
- **Why containers/microVMs and not a shared interpreter sandbox:** we must support *every* major language's native test runner, which rules out any single-language in-process sandbox. Isolation must be OS-level.

## Code graph

- **Parsing:** Tree-sitter for language-agnostic parsing across all target ecosystems — one grammar per language, mature, fast, incremental. This is the single best leverage point for broad language support on the analysis side.
- **Symbol/type resolution:** lean on each ecosystem's existing tooling (LSP servers, type checkers) rather than reimplementing type resolution per language.
- **Storage:** a graph representation queryable for blast-radius. Start pragmatic — a well-indexed relational or embedded graph store — before reaching for a heavy dedicated graph database. Blast-radius queries are the workload to optimize; pick the store that serves those fast and self-hosts cleanly.
- **Incremental indexing:** update only the changed region + neighbors. Never full re-index on commit.

## Deterministic scaffolding (borrowed precision — free wins)

Layer existing tools *under* the LLM so the model isn't asked to rediscover what a linter already knows:

- **Linters/SAST per ecosystem** (ESLint, Ruff, Semgrep, Gitleaks, gosec, etc.).
- **Type checkers** (tsc, mypy, etc.).
- These pre-filter and enrich; the triad reasons about what they surface plus what the graph reveals. This is a known, effective pattern — free precision that costs no model tokens.

## Model layer

- **Router:** kernel-provided cascade. Buggies' policy: cheap/local model filters obvious noise → mid-tier confirms candidates → frontier model runs only on survivors that reach the PR triad.
- **Caching:** fingerprint-keyed (kernel). Same code → same cached reasoning → same finding. This is both a cost lever and the determinism mechanism.
- **Provider posture:** model-agnostic behind the router so you can move as price/quality shifts, and so self-hosted customers can bring their own keys or point at a local model for the cheap tiers.
- **Local model for the save/keystroke tier:** a small local model keeps the tightest loop cheap and private.

## Integrations

- **Git hosts:** GitHub App first (largest footprint, cleanest App model), then GitLab and Bitbucket. Also a CLI, an IDE extension, and an **MCP server** so Buggies works inside agentic coding tools.
- **Error trackers (right door):** Sentry first (largest developer footprint), then Datadog, Bugsnag, generic OpenTelemetry. All read-only.
- **CI:** a status check / gate that reports the binary "reproduced blocking finding: yes/no."

## Deployment modes

- **Hosted SaaS** for the mid-market default.
- **Self-hosted / BYO-key** as a first-class mode from day one — the finding store, sandbox, and graph all run in the customer's cloud; only optional model calls leave (and can be pointed at the customer's own model endpoint). This is a hard requirement for a meaningful slice of buyers and cannot be bolted on later.

## Explicitly rejected (and why)

- **A single-language in-process sandbox** — rejected: can't run every ecosystem's native test runner. Isolation must be OS-level.
- **Building our own APM / telemetry SDK** — rejected: puts us in the request hot path, owns the PII problem, competes with Datadog, months of install friction. Consume telemetry read-only instead.
- **A heavyweight dedicated graph DB on day one** — deferred: blast-radius queries can be served by a simpler indexed store first; add specialized graph infra only when query performance demands it.
- **Full-application sandbox in v0** — deferred to v1 (locked decision): units-first ships faster and covers most correctness bugs.
- **One giant model on every event** — rejected: negative gross margin. Cascade or die.
