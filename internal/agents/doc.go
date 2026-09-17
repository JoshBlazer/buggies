// Package agents supplies Buggies' triad agents to the kernel state machine.
//
// Personality: conservative, low-noise, stop early. The proposer prefers
// silence to speculation; the skeptic is paid to kill; the prover writes the
// smallest test that fails. Prompts live here, never in the kernel.
package agents
