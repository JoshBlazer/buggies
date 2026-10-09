---
name: correctness-repro
description: Recognise and reproduce correctness defects — wrong results, off-by-one, null/empty mishandling, bad branches.
applies_to: correctness
---

# Correctness defects

## How to recognise one

A correctness defect makes a function return a wrong value or throw where it
should not, for an input the code is expected to handle. Look for:

- **Off-by-one / boundary**: `<=` vs `<`, `length` vs `length - 1`, first/last
  element, empty collection, single-element collection.
- **Null / undefined / empty**: a value that can be absent is used without a
  guard; an empty string/array/map takes a branch written for the non-empty case.
- **Inverted or wrong condition**: a negated check, swapped branches, `&&` where
  `||` was meant, a comparison against the wrong variable.
- **Wrong return shape**: returns the container when a member was meant, the
  index when the value was meant, or a stale value after mutation.

## How to write the reproduction

- Call the changed function **directly** with the smallest concrete input that
  exposes the defect. Prefer the boundary input (empty, zero, first, last).
- Assert the **exact** correct value, not just "not equal to the buggy value".
- One assertion. No loops over many cases — pick the single most revealing input.
- The test must fail by assertion on the current code, and pass once the obvious
  fix is applied. If you cannot name the exact input and expected value, the
  candidate is not reproducible — drop it.
