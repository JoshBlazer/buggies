// Package eval is Buggies' precision/recall harness. It runs `review` over a
// set of seeded-bug fixtures, each of which declares the finding(s) a correct
// review must reproduce, then scores the run: true positives (a seeded bug
// reproduced), false positives (a finding nobody asked for — noise), and false
// negatives (a seeded bug missed).
//
// The harness exists to put real numbers on the product claim — "high
// precision, low noise" — so regressions in that claim fail CI instead of
// going unnoticed. It drives the SAME pipeline the `buggies review` command
// drives (real graph, real triad, real sandbox reproduction), so the metrics
// reflect the shipped behaviour, not a mock.
//
// Because reproduction runs real code in the sandbox, a full eval needs a
// container runtime: gVisor (runsc) in CI/production, or runc locally with
// BUGGIES_ALLOW_RUNC=1. The matching and scoring logic (this file and
// match.go) is pure and is unit-tested without any sandbox.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshblazer/kernel/finding"
)

// expectFile is the per-fixture manifest, stored as expect.json at the root of
// each fixture directory. It is the machine-readable contract between a seeded
// bug and the harness.
const expectFile = "expect.json"

// Fixture is one seeded-bug repository and the findings a correct review must
// (and must not) produce for it.
type Fixture struct {
	// Name identifies the fixture; defaults to the directory name.
	Name string `json:"name"`
	// Repo is the owner/repo passed to the pipeline (used in finding IDs).
	Repo string `json:"repo"`
	// Description is a human note about the seeded bug.
	Description string `json:"description"`
	// Ecosystem is the adapter to use. Only "node" is supported in v0; empty
	// defaults to "node".
	Ecosystem string `json:"ecosystem,omitempty"`

	// Expect lists the findings a correct review MUST reproduce. An empty list
	// marks a negative fixture: a clean change where any finding is noise.
	// Each expectation is matched to at most one actual finding.
	Expect []Matcher `json:"expect"`
	// Allow lists findings that are real but not required — a secondary true
	// bug the fixture is not scoring on, say. An actual finding matched by an
	// Allow matcher counts as neither a true nor a false positive.
	Allow []Matcher `json:"allow,omitempty"`

	// Dir is the absolute path to the fixture directory. Set by Load.
	Dir string `json:"-"`
}

// Matcher describes a finding by what it IS, independent of the exact wording
// the model chose. All set fields must hold for a finding to match; unset
// fields are wildcards. Matching deliberately avoids the finding's stable ID
// (which folds in a normalised code excerpt and the model-chosen symbol) and
// the exact line number, both of which are too brittle to hand-author.
type Matcher struct {
	// Type is the risk class and is required: "correctness" | "contract" |
	// "resilience" | "security".
	Type string `json:"type"`
	// Path is the repository-relative file (forward slashes) and is required.
	Path string `json:"path"`
	// Symbol, when set, must equal the enclosing symbol the pipeline recorded.
	Symbol string `json:"symbol,omitempty"`
	// MinImpact, when set, is the least severe impact that still matches:
	// "cosmetic" | "degraded" | "data-loss" | "exploitable".
	MinImpact string `json:"minImpact,omitempty"`
	// Blocking, when true, requires the finding to meet the merge-gate bar
	// (reproduced AND impact >= data-loss).
	Blocking bool `json:"blocking,omitempty"`
	// LineRange, when set to [lo, hi], requires the finding's line to fall
	// within [lo, hi] inclusive. Use a generous range; the fingerprint ignores
	// lines on purpose.
	LineRange []int `json:"lineRange,omitempty"`
	// ClaimRegex, when set, must match the finding's one-line claim
	// (case-insensitive unless the pattern opts out).
	ClaimRegex string `json:"claimRegex,omitempty"`

	claimRe *regexp.Regexp // compiled ClaimRegex, cached by compile()
}

// Load reads every fixture under dir (each a subdirectory containing an
// expect.json). It validates each manifest and compiles its matchers so a bad
// fixture fails fast rather than mid-run.
func Load(dir string) ([]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: read fixtures dir: %w", err)
	}
	var out []Fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fxDir := filepath.Join(dir, e.Name())
		manifest := filepath.Join(fxDir, expectFile)
		if _, err := os.Stat(manifest); err != nil {
			continue // a directory without a manifest is not a fixture
		}
		fx, err := loadOne(fxDir, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, fx)
	}
	return out, nil
}

func loadOne(fxDir, dirName string) (Fixture, error) {
	b, err := os.ReadFile(filepath.Join(fxDir, expectFile))
	if err != nil {
		return Fixture{}, err
	}
	var fx Fixture
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err != nil {
		return Fixture{}, fmt.Errorf("eval: %s/%s: %w", dirName, expectFile, err)
	}
	if fx.Name == "" {
		fx.Name = dirName
	}
	if fx.Repo == "" {
		return Fixture{}, fmt.Errorf("eval: %s: repo is required", fx.Name)
	}
	if fx.Ecosystem == "" {
		fx.Ecosystem = "node"
	}
	fx.Dir = fxDir
	for i := range fx.Expect {
		if err := fx.Expect[i].compile(fmt.Sprintf("%s expect[%d]", fx.Name, i)); err != nil {
			return Fixture{}, err
		}
	}
	for i := range fx.Allow {
		if err := fx.Allow[i].compile(fmt.Sprintf("%s allow[%d]", fx.Name, i)); err != nil {
			return Fixture{}, err
		}
	}
	return fx, nil
}

func (m *Matcher) compile(where string) error {
	if m.Type == "" {
		return fmt.Errorf("eval: %s: type is required", where)
	}
	if _, ok := parseType(m.Type); !ok {
		return fmt.Errorf("eval: %s: unknown type %q", where, m.Type)
	}
	if m.Path == "" {
		return fmt.Errorf("eval: %s: path is required", where)
	}
	if m.MinImpact != "" {
		if _, ok := parseImpact(m.MinImpact); !ok {
			return fmt.Errorf("eval: %s: unknown minImpact %q", where, m.MinImpact)
		}
	}
	if len(m.LineRange) != 0 && len(m.LineRange) != 2 {
		return fmt.Errorf("eval: %s: lineRange must be [lo, hi]", where)
	}
	if m.ClaimRegex != "" {
		re, err := regexp.Compile(m.ClaimRegex)
		if err != nil {
			return fmt.Errorf("eval: %s: claimRegex: %w", where, err)
		}
		m.claimRe = re
	}
	return nil
}

// matches reports whether f satisfies every set field of m.
func (m *Matcher) matches(f finding.Finding) bool {
	wantType, _ := parseType(m.Type)
	if f.Type != wantType {
		return false
	}
	if slashPath(f.Location.Path) != slashPath(m.Path) {
		return false
	}
	if m.Symbol != "" && f.Location.Symbol != m.Symbol {
		return false
	}
	if m.MinImpact != "" {
		min, _ := parseImpact(m.MinImpact)
		if f.Impact < min {
			return false
		}
	}
	if m.Blocking && !f.Blocking() {
		return false
	}
	if len(m.LineRange) == 2 {
		if f.Location.Line < m.LineRange[0] || f.Location.Line > m.LineRange[1] {
			return false
		}
	}
	if m.claimRe != nil && !m.claimRe.MatchString(f.Claim) {
		return false
	}
	return true
}

func slashPath(p string) string { return strings.ReplaceAll(p, "\\", "/") }

func parseType(s string) (finding.Type, bool) {
	switch finding.Type(strings.ToLower(strings.TrimSpace(s))) {
	case finding.Correctness:
		return finding.Correctness, true
	case finding.Contract:
		return finding.Contract, true
	case finding.Resilience:
		return finding.Resilience, true
	case finding.Security:
		return finding.Security, true
	}
	return "", false
}

func parseImpact(s string) (finding.Impact, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cosmetic":
		return finding.Cosmetic, true
	case "degraded":
		return finding.Degraded, true
	case "data-loss", "dataloss":
		return finding.DataLoss, true
	case "exploitable":
		return finding.Exploitable, true
	}
	return 0, false
}
