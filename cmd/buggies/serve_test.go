package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshblazer/kernel/sandbox"
)

// writeFiles creates each named file (empty) under dir.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelectAdapter(t *testing.T) {
	ctx := context.Background()
	cands := candidateAdapters()

	tests := []struct {
		name  string
		files []string
		want  sandbox.Ecosystem // "" means no adapter should match
	}{
		{"node only", []string{"package.json"}, sandbox.Node},
		{"python pyproject", []string{"pyproject.toml"}, sandbox.Python},
		{"python requirements", []string{"requirements.txt"}, sandbox.Python},
		{"python setup.py", []string{"setup.py"}, sandbox.Python},
		{"polyglot node wins", []string{"package.json", "pyproject.toml"}, sandbox.Node},
		{"empty tree", nil, ""},
		{"unrelated files", []string{"README.md", "main.go"}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tc.files...)

			got, err := selectAdapter(ctx, cands, dir, "HEAD")
			if err != nil {
				t.Fatalf("selectAdapter: %v", err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatalf("expected no adapter, got %s", got.Ecosystem())
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %s adapter, got nil (neutral skip)", tc.want)
			}
			if got.Ecosystem() != tc.want {
				t.Fatalf("selected %s, want %s", got.Ecosystem(), tc.want)
			}
		})
	}
}

// Every candidate must advertise a distinct ecosystem so registering them in
// the runtime Adapters map by Ecosystem() key never silently drops one.
func TestCandidateAdaptersDistinctEcosystems(t *testing.T) {
	seen := map[sandbox.Ecosystem]bool{}
	cands := candidateAdapters()
	if len(cands) == 0 {
		t.Fatal("candidateAdapters is empty")
	}
	for _, a := range cands {
		eco := a.Ecosystem()
		if seen[eco] {
			t.Fatalf("duplicate ecosystem %s among candidate adapters", eco)
		}
		seen[eco] = true
	}
	if !seen[sandbox.Node] || !seen[sandbox.Python] {
		t.Fatalf("expected both Node and Python candidates, got %v", seen)
	}
}
