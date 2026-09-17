// Package store is Buggies' own implementation of kernel/finding.Store.
//
// It is a breach target — a queue of unpatched, verified bugs in customer
// code. Encrypt at rest, keep retention minimal, control access. SQLite in
// v0 (self-host friendly); Postgres when scale demands it.
package store
