package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshblazer/kernel/sandbox"

	"github.com/joshblazer/buggies/internal/pipeline"
	"github.com/joshblazer/buggies/internal/unattended"
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

// classifyReviewError must turn infrastructure and can't-review conditions
// into neutral skips (posted to the PR, job not retried) while passing real
// faults through unchanged (job fails). A sandbox-unavailable job that used to
// die silently now leaves a non-blocking "sandbox unavailable" note.
func TestClassifyReviewError(t *testing.T) {
	asSkip := func(t *testing.T, err error) *unattended.SkipReview {
		t.Helper()
		var skip *unattended.SkipReview
		if !errors.As(err, &skip) {
			t.Fatalf("expected a *SkipReview, got %T: %v", err, err)
		}
		return skip
	}

	t.Run("sandbox unavailable becomes a neutral skip", func(t *testing.T) {
		// The real error wraps errSandboxUnavailable (as serveReview does).
		in := fmt.Errorf("%w: docker info: exit status 1", errSandboxUnavailable)
		skip := asSkip(t, classifyReviewError(in))
		if !strings.Contains(skip.Note, "sandbox unavailable") {
			t.Fatalf("note = %q, want it to mention sandbox unavailable", skip.Note)
		}
	})

	t.Run("unsupported ecosystem becomes a neutral skip", func(t *testing.T) {
		skip := asSkip(t, classifyReviewError(&pipeline.ErrUnsupportedProject{Ecosystem: "go"}))
		if !strings.Contains(skip.Note, "go") {
			t.Fatalf("note = %q, want it to name the ecosystem", skip.Note)
		}
	})

	t.Run("a generic error is returned unchanged so the job fails", func(t *testing.T) {
		in := errors.New("boom")
		if got := classifyReviewError(in); got != in {
			t.Fatalf("got %v, want the original error returned unchanged", got)
		}
	})

	t.Run("nil stays nil", func(t *testing.T) {
		if got := classifyReviewError(nil); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
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
