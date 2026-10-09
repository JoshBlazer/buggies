---
name: contract-repro
description: Recognise and reproduce contract defects — a change that breaks an existing caller shown in the blast radius.
applies_to: contract
---

# Contract defects

## How to recognise one

A contract defect is a change that silently breaks a **caller** that the blast
radius shows depends on the old behaviour. The defect is not in the changed
function alone — it is in the mismatch between the new behaviour and what a
caller already relies on. Look for:

- **Signature drift**: parameter added/removed/reordered, a required arg made
  optional (or the reverse), a type narrowed or widened.
- **Return-shape drift**: returns a different type, a different field, `null`
  where a value was guaranteed, a Promise where a value was synchronous.
- **Behavioural drift**: same signature, different meaning — a flag inverted, a
  default changed, an error now thrown (or now swallowed), ordering changed.
- **Matching/lookup drift**: a comparison or key that callers depend on now
  matches a different set (e.g. canonicalising away an alias a caller passes).

Use the blast radius: find a concrete caller and the exact value or call shape
it passes, then show that call now gets the wrong result.

## How to write the reproduction

- Drive the defect **through the caller's usage pattern**, not just the changed
  function, so the test demonstrates the broken contract.
- Pick the specific input the caller is shown to use (a flag, a key, an arg
  shape visible in the blast radius) and assert the behaviour the caller expects.
- One assertion on a concrete expected value. No mocking of the code under test.
- If no caller in the blast radius actually depends on the changed behaviour,
  there is no contract break — drop the candidate.
