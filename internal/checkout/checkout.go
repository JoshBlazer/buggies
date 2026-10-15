// Package checkout materializes a working tree for a pull request so the
// unattended worker can review code it has never seen on disk. It fetches the
// PR head ref from the BASE repository — GitHub exposes refs/pull/<N>/head for
// same-repo and fork PRs alike, so the fork's own clone URL and credentials are
// never needed — and checks it out detached at the head commit. The base
// commit is ensured present too, because the review diffs base..head.
//
// A fork PR is still untrusted code: this package only lays the files down. The
// sandbox (kernel) is what isolates execution, and the fork-safe environment
// (internal/security) is what keeps secrets out of the model subprocess.
package checkout

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Spec describes one working tree to produce.
type Spec struct {
	// RepoURL is the base repository's git URL. For a private repo the caller
	// embeds the installation token, e.g.
	// https://x-access-token:<token>@github.com/owner/repo.git
	RepoURL string
	// Ref is the head ref to fetch, e.g. "refs/pull/42/head" (or a branch name
	// in tests). It must resolve to HeadSHA on the remote.
	Ref string
	// HeadSHA is the commit the review runs on; the checkout is verified against
	// it so a moved ref can never be reviewed as a different commit.
	HeadSHA string
	// BaseSHA is the diff base; ensured present in the tree for base..head.
	BaseSHA string
	// Dir is the working tree directory (created if absent, reused if present).
	Dir string
}

// Materialize produces (or refreshes) the working tree for spec and returns its
// directory, checked out detached at spec.HeadSHA. Reusing an existing Dir is
// safe: the fetch+checkout is idempotent, so a redelivered job costs a fetch,
// not a fresh clone.
func Materialize(ctx context.Context, spec Spec) (string, error) {
	if spec.RepoURL == "" || spec.Ref == "" || spec.Dir == "" {
		return "", fmt.Errorf("checkout: RepoURL, Ref and Dir are required")
	}
	if err := os.MkdirAll(spec.Dir, 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(spec.Dir, ".git")); err != nil {
		if err := git(ctx, spec.Dir, "init", "-q"); err != nil {
			return "", err
		}
	}
	// Fetch the head ref in full (not --depth 1): the review needs the base
	// commit reachable for the diff, and a shallow head rarely contains it.
	if err := git(ctx, spec.Dir, "fetch", "--quiet", "--no-tags", spec.RepoURL, spec.Ref); err != nil {
		return "", fmt.Errorf("checkout: fetch %s: %w", spec.Ref, err)
	}
	if err := git(ctx, spec.Dir, "checkout", "--quiet", "--force", "--detach", "FETCH_HEAD"); err != nil {
		return "", fmt.Errorf("checkout: checkout head: %w", err)
	}
	// Ensure the base commit is present too. It is usually already reachable
	// from the head branch; fetch it explicitly only if not.
	if spec.BaseSHA != "" && !hasCommit(ctx, spec.Dir, spec.BaseSHA) {
		if err := git(ctx, spec.Dir, "fetch", "--quiet", "--no-tags", spec.RepoURL, spec.BaseSHA); err != nil {
			return "", fmt.Errorf("checkout: fetch base %s: %w", short(spec.BaseSHA), err)
		}
	}
	if spec.HeadSHA != "" {
		got, err := output(ctx, spec.Dir, "rev-parse", "HEAD")
		if err != nil {
			return "", err
		}
		if !shaMatch(got, spec.HeadSHA) {
			return "", fmt.Errorf("checkout: head is %s, expected %s (ref %s moved?)",
				short(got), short(spec.HeadSHA), spec.Ref)
		}
	}
	// The sandbox bind-mounts this tree read-only and reads it as an
	// unprivileged, non-owner uid (65534). The tree is created 0700 and git
	// writes contents at the daemon's umask, so that uid may be unable to read
	// it — the copy-in step then fails with "Permission denied" and nothing can
	// be reproduced. Make the tree world-readable (dirs +rx, files +r) so the
	// sandbox user can read it. This is safe: the tree holds only the PR's own
	// (untrusted, already public) source, and its parent dir stays 0700, so no
	// other local user gains reach and no secret (store key, graph.db, tokens,
	// which live outside this tree) is touched.
	if err := makeSandboxReadable(spec.Dir); err != nil {
		return "", fmt.Errorf("checkout: make tree readable: %w", err)
	}
	return spec.Dir, nil
}

// makeSandboxReadable grants other-read on every regular file and other-read+
// execute on every directory under dir, so the sandbox's unprivileged uid can
// read the mounted tree. Symlinks are skipped: chmod follows them, and a
// malicious PR could point one at a host path outside the tree.
func makeSandboxReadable(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		want := mode | 0o044 // o+r, g+r
		if d.IsDir() {
			want |= 0o055 // o+rx, g+rx so the dir is traversable
		}
		if want == mode {
			return nil
		}
		return os.Chmod(path, want)
	})
}

// hasCommit reports whether sha names a commit object already in Dir.
func hasCommit(ctx context.Context, dir, sha string) bool {
	return git(ctx, dir, "cat-file", "-e", sha+"^{commit}") == nil
}

// shaMatch is true when a and b name the same commit, tolerating one being an
// abbreviation of the other (webhooks sometimes carry short SHAs).
func shaMatch(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a == b || strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// git runs a git command in dir, discarding stdout; it returns an error that
// includes git's stderr so a failure is diagnosable.
func git(ctx context.Context, dir string, args ...string) error {
	_, err := output(ctx, dir, args...)
	return err
}

// output runs a git command in dir and returns trimmed stdout.
func output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
