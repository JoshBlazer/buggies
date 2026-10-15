package checkout

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// TestMaterializeTreeIsSandboxReadable proves the materialized tree is readable
// by a non-owner, unprivileged uid — the sandbox runs as uid 65534 and copies
// the bind-mounted tree in, so a tree left at 0700 makes that copy fail with
// "Permission denied" and no finding can ever be reproduced (the serve-path
// false-negative). Every directory must carry o+rx and every file o+r.
func TestMaterializeTreeIsSandboxReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file permissions")
	}
	repo, baseSHA, headSHA := gitInit(t)
	// Create the destination at the restrictive mode the daemon uses, to prove
	// Materialize relaxes it rather than inheriting it.
	dir := filepath.Join(t.TempDir(), "checkout", headSHA)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(context.Background(), Spec{
		RepoURL: repo, Ref: "pr", HeadSHA: headSHA, BaseSHA: baseSHA, Dir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		perm := info.Mode().Perm()
		if perm&0o004 == 0 {
			t.Errorf("%s is not other-readable: %o", path, perm)
		}
		if d.IsDir() && perm&0o001 == 0 {
			t.Errorf("dir %s is not other-traversable: %o", path, perm)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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
