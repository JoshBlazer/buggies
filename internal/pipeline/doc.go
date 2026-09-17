// Package pipeline is the left door: diff → SCOPE (graph blast radius) →
// TRIAD (propose/attack/prove via kernel) → VERIFY (sandbox, units-first) →
// STORE (dedup, stable ID, lifecycle).
//
// Invariant: nothing leaves this package as a Finding unless the kernel triad
// reported it Confirmed, i.e. reproduced in the sandbox.
package pipeline
