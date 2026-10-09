package checkout

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInit builds a throwaway "remote" repo with a base commit and a head commit
// on branch pr, returning the repo path and the two SHAs. It stands in for the
// GitHub base repo and its refs/pull/N/head during the test (no network).
func gitInit(t *testing.T) (repo, baseSHA, headSHA string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "base")
	baseSHA = run("rev-parse", "HEAD")
	run("checkout", "-q", "-b", "pr")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("head\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "head")
	headSHA = run("rev-parse", "HEAD")
	return repo, baseSHA, headSHA
}

// TestMaterializeChecksOutHeadWithBasePresent proves the worker can lay down a
// PR: head is checked out at the expected SHA and the base commit is reachable
// so the review can diff base..head.
func TestMaterializeChecksOutHeadWithBasePresent(t *testing.T) {
	repo, baseSHA, headSHA := gitInit(t)
	dir := filepath.Join(t.TempDir(), "ws")

	got, err := Materialize(context.Background(), Spec{
		RepoURL: repo, Ref: "pr", HeadSHA: headSHA, BaseSHA: baseSHA, Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("dir = %q, want %q", got, dir)
	}
	// Head content is present.
	b, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil || strings.TrimSpace(string(b)) != "head" {
		t.Fatalf("working tree not at head: content=%q err=%v", b, err)
	}
	// Both commits are reachable → base..head diff will work.
	if !hasCommit(context.Background(), dir, headSHA) {
		t.Error("head commit missing")
	}
	if !hasCommit(context.Background(), dir, baseSHA) {
		t.Error("base commit missing; diff base..head would fail")
	}
}

// TestMaterializeIsIdempotent proves a redelivered job refreshes the same tree
// rather than failing — the worker can re-run safely.
func TestMaterializeIdempotent(t *testing.T) {
	repo, baseSHA, headSHA := gitInit(t)
	dir := filepath.Join(t.TempDir(), "ws")
	sp := Spec{RepoURL: repo, Ref: "pr", HeadSHA: headSHA, BaseSHA: baseSHA, Dir: dir}
	for i := 0; i < 2; i++ {
		if _, err := Materialize(context.Background(), sp); err != nil {
			t.Fatalf("materialize #%d: %v", i+1, err)
		}
	}
}

// TestMaterializeRejectsMovedRef proves a ref that resolves to a different
// commit than the webhook claimed is refused — Buggies never reviews a commit
// other than the one it was asked to.
func TestMaterializeRejectsMovedRef(t *testing.T) {
	repo, baseSHA, _ := gitInit(t)
	dir := filepath.Join(t.TempDir(), "ws")
	_, err := Materialize(context.Background(), Spec{
		RepoURL: repo, Ref: "pr", HeadSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", BaseSHA: baseSHA, Dir: dir,
	})
	if err == nil {
		t.Fatal("expected a head-SHA mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Fatalf("error = %v, want a head-mismatch message", err)
	}
}
