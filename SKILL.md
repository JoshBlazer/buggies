---
name: buggies
description: Review the current branch for reproducible bugs before opening a pull request. Use when you have staged or committed a change and are about to open a PR, or when asked to check a diff for correctness/contract defects with proof. Buggies only reports findings it reproduced in a sandbox (a failing test), so a finding is true by construction.
---

# Using Buggies before you open a PR

Buggies catches a bug before it ships and proves it with a failing test. Run it
on your change before opening the pull request, so you fix reproduced defects
first instead of learning about them in review.

## When to use it

- You've committed a change and are about to open a PR.
- You want a second opinion on a diff that is backed by proof, not guesses.
- You want to know what else your change can affect (its blast radius).

## The tools (over the `buggies mcp` MCP server)

Start the server for the repo you're in: `buggies mcp --name <owner/repo>`.

1. **`blast_radius`** `{repo, base, head}` — what the change can affect (callers,
   dependents). Call this first to understand scope.
2. **`review`** `{repo, base, head}` — review the diff; returns only findings
   that were **reproduced in the sandbox**. Each carries a failing test and a
   suggested fix. An empty result means nothing reproducible — the intended
   outcome for most changes.
3. **`get_finding`** `{id}` — fetch a finding and its reproduction (the failing
   test) by stable id, to read or run the proof yourself.
4. **`dismiss`** `{id, reason}` — mark a finding dismissed if it's wrong; it
   stops being reported and feeds calibration.

## How to act on the results

- Treat a **reproduced** finding as real: run its failing test to confirm, apply
  (or adapt) the suggested fix, and re-run `review` — the same bug has a stable
  id, so a fixed one won't come back as new.
- Rank by **verification first, severity second**: a reproduced medium outranks
  an unverified critical. Buggies does not report unreproduced guesses.
- Only then open the PR.
