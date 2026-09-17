// Package prcomment renders a finding as an inline PR comment:
//
//   - one-line claim
//   - type + level badge, e.g. "Correctness · Reproduced × Data-loss"
//   - the proof, collapsed: a failing test the reviewer can run (never omitted)
//   - suggested fix as a committable diff
//   - honest confidence line
//
// Ranking on this and every surface: verification status first, severity second.
package prcomment
