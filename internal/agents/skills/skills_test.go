package skills

import (
	"testing"

	"github.com/joshblazer/kernel/finding"
)

func TestLoadAndSelectiveMatch(t *testing.T) {
	set, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	// Both v0 bug classes have a skill, keyed by the type they apply to.
	for _, typ := range []finding.Type{finding.Correctness, finding.Contract} {
		sk, ok := set.For(typ)
		if !ok {
			t.Fatalf("no skill for %q", typ)
		}
		if sk.Name == "" || sk.Description == "" || sk.Body == "" {
			t.Errorf("skill for %q has empty fields: %+v", typ, sk)
		}
		if sk.AppliesTo != typ {
			t.Errorf("skill applies_to = %q, want %q", sk.AppliesTo, typ)
		}
	}

	// Selective loading: a type with no skill matches nothing (the proposer and
	// out-of-scope types never pull in unrelated instructions).
	if _, ok := set.For(finding.Security); ok {
		t.Error("expected no skill for security in v0")
	}

	// The correctness skill's body is specific to its class, not generic.
	if sk, _ := set.For(finding.Correctness); !contains(sk.Body, "Off-by-one") {
		t.Error("correctness skill body missing class-specific guidance")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
