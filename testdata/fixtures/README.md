# Seeded-bug fixtures — the eval harness corpus

Each subdirectory here is one **fixture**: a tiny repository with a bug (or,
for a negative fixture, a deliberately clean change) seeded between two commits,
plus a machine-readable declaration of the finding(s) a correct review must —
and must not — produce. `cmd/buggies-eval` (package `internal/eval`) runs the
real `review` pipeline over every fixture and scores precision/recall against
these declarations.

## Layout

```
testdata/fixtures/<name>/
  expect.json      # the contract (see below)
  base/            # the full source tree at the BASE commit (before the bug)
  head/            # the full source tree at the HEAD commit (bug introduced)
```

The harness builds a throwaway git repo at run time: it commits `base/` as the
base revision, then commits `head/` as the head revision, and reviews
`base..head` with the working tree at head — exactly the shape `buggies review`
sees for a PR. Keep both trees complete (not a diff); unchanged files appear
identically in both. Trees must be a supported ecosystem — v0 is Node, so each
needs a `package.json`.

## `expect.json`

```jsonc
{
  "name": "gold-tier-discount",   // optional; defaults to the directory name
  "repo": "acme/shop",            // required; owner/repo used in finding IDs
  "description": "…",             // human note about the seeded bug
  "ecosystem": "node",            // optional; defaults to "node"

  "expect": [                     // findings a correct review MUST reproduce
    {
      "type": "correctness",      // required: correctness|contract|resilience|security
      "path": "src/discount.js",  // required: repo-relative, forward slashes
      "symbol": "applyDiscount",  // optional: enclosing symbol must equal this
      "minImpact": "data-loss",   // optional: cosmetic|degraded|data-loss|exploitable
      "blocking": true,           // optional: must meet the merge-gate bar
      "lineRange": [1, 5],        // optional: finding line within [lo, hi]
      "claimRegex": "(?i)gold"    // optional: regex over the one-line claim
    }
  ],

  "allow": []                     // optional: real-but-not-scored findings;
                                  // matched findings count as neither TP nor FP
}
```

An empty `expect` array marks a **negative fixture**: a clean change where any
finding is noise and counts against precision. `clean-refactor` is one.

### Why matchers describe the bug, not the exact output

A finding's stable ID folds in a normalised code excerpt and the model-chosen
symbol, and its line number shifts with unrelated edits — none of that is
hand-authorable. Matchers instead pin the stable facts (type, file, enclosing
symbol, severity floor, a loose line window, a claim keyword). Every set field
must hold; unset fields are wildcards. Keep `lineRange` generous and
`claimRegex` about the concept, not the wording.

## Scoring

For each fixture the harness runs a one-to-one assignment: every expectation
claims the first unclaimed finding it matches.

- **TP** — an expectation that matched a finding (seeded bug reproduced).
- **FN** — an expectation with no match (bug missed).
- **FP** — a produced finding no expectation (and no `allow`) covers (noise).

Across fixtures it micro-averages precision = TP/(TP+FP), recall = TP/(TP+FN),
and F1, plus a per-type breakdown and a **blocking-only** slice (findings at the
merge-gate bar, where precision matters most). A fixture passes iff FN=0 and
FP=0; `buggies-eval` exits non-zero if any fixture fails.

## Running

Reproduction runs real code in the sandbox, so a container runtime is required:

```sh
# CI / production: gVisor (runsc)
buggies-eval --fixtures testdata/fixtures

# local dev without gVisor
buggies-eval --fixtures testdata/fixtures --allow-runc   # or BUGGIES_ALLOW_RUNC=1

buggies-eval --only gold-tier-discount --json            # one fixture, JSON report
```

Model access comes from `ANTHROPIC_API_KEY` or an `ant auth login` profile,
exactly as `buggies review`.

## Adding a fixture

1. `mkdir testdata/fixtures/<name>` with `base/` and `head/` trees (include a
   `package.json`). Seed one bug; keep it small and deterministic.
2. Write `expect.json`. For a bug fixture, describe the finding; for a clean
   change, leave `expect` empty.
3. `go test ./internal/eval/...` validates the manifest loads and compiles.
4. `buggies-eval --only <name>` confirms the real pipeline scores it as intended.
