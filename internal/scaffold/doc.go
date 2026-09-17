// Package scaffold runs deterministic tools (eslint, tsc, and later ruff,
// mypy, semgrep) under the model so the triad is never asked to rediscover
// what a linter already knows. Output enriches triad.Scope; it never becomes a
// finding on its own.
package scaffold
