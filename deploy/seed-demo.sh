#!/usr/bin/env bash
# seed-demo.sh — turn the eval fixtures into public demo pull requests.
#
# For each fixture under testdata/fixtures/, this creates (or reuses) a public
# repo under $OWNER, pushes the fixture's base/ tree as `main`, pushes its head/
# tree as a branch, and opens a pull request base <- head. Installing the
# Buggies GitHub App on these repos then produces a real review on each PR that
# anyone can click through on github.com — the zero-steps-for-viewers demo.
#
# One repo per fixture (not subfolders): each fixture's root IS a project
# (its package.json sits at the root), which is exactly the shape the review
# pipeline and the sandbox expect. A monorepo-with-subfolders demo would ask
# the executor to resolve a nested project root, which v0 does not promise.
#
# It is idempotent: re-running force-updates the trees and reopens the PR, so
# you can reset the demo to a known state at any time.
#
# Requirements: `gh` (authenticated: `gh auth status`) and `git`. The App must
# be installed on each repo for a review to run — do that once in the App's
# "Install" settings after the first run creates the repos, or install it
# org-wide on $OWNER.
#
# Usage:
#   OWNER=my-demo-org ./deploy/seed-demo.sh            # all fixtures
#   OWNER=my-demo-org ./deploy/seed-demo.sh gold-tier-discount page-drops-last
#
# Env:
#   OWNER       (required) the GitHub user or org to create the demo repos under.
#   PREFIX      repo name prefix (default: "buggies-demo-").
#   FIXTURES    fixtures dir (default: testdata/fixtures, relative to repo root).
#   PR_BRANCH   the PR head branch name (default: "proposed-change").
set -euo pipefail

: "${OWNER:?set OWNER to the GitHub user/org for the demo repos}"
PREFIX="${PREFIX:-buggies-demo-}"
PR_BRANCH="${PR_BRANCH:-proposed-change}"

# Resolve the repo root from this script's location so it runs from anywhere.
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
FIXTURES="${FIXTURES:-$root/testdata/fixtures}"

command -v gh  >/dev/null || { echo "gh not found (https://cli.github.com/)" >&2; exit 1; }
command -v git >/dev/null || { echo "git not found" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "run 'gh auth login' first" >&2; exit 1; }

# Which fixtures: args, or every directory with an expect.json.
names=("$@")
if [ "${#names[@]}" -eq 0 ]; then
  while IFS= read -r d; do names+=("$(basename "$d")"); done \
    < <(find "$FIXTURES" -mindepth 1 -maxdepth 1 -type d | sort)
fi

# copy_tree SRC DST — mirror SRC into DST, clearing DST's tracked files first
# but preserving its .git directory.
copy_tree() {
  local src="$1" dst="$2"
  find "$dst" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
  cp -a "$src/." "$dst/"
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for name in "${names[@]}"; do
  fdir="$FIXTURES/$name"
  [ -d "$fdir/base" ] && [ -d "$fdir/head" ] || { echo "skip $name: no base/ or head/" >&2; continue; }

  repo="$OWNER/${PREFIX}${name}"
  desc="Buggies demo: $name"
  if [ -f "$fdir/expect.json" ]; then
    # Pull the human description out of expect.json for the repo + PR body.
    d="$(grep -o '"description"[[:space:]]*:[[:space:]]*"[^"]*"' "$fdir/expect.json" | sed 's/.*: *"//; s/"$//')"
    [ -n "$d" ] && desc="$d"
  fi

  echo "==> $repo"
  gh repo view "$repo" >/dev/null 2>&1 \
    || gh repo create "$repo" --public --description "Buggies demo — $name" >/dev/null

  work="$tmp/$name"
  mkdir -p "$work"
  git -C "$work" init -q -b main
  git -C "$work" remote add origin "https://github.com/$repo.git"

  # main = base tree.
  copy_tree "$fdir/base" "$work"
  git -C "$work" add -A
  git -C "$work" -c user.name="Buggies Demo" -c user.email="demo@example.com" \
      commit -q -m "seed: baseline project"
  git -C "$work" push -q -f origin main

  # PR branch = head tree (the proposed change that introduces the behaviour).
  git -C "$work" checkout -q -b "$PR_BRANCH"
  copy_tree "$fdir/head" "$work"
  git -C "$work" add -A
  if git -C "$work" diff --cached --quiet; then
    echo "    (no diff base->head; nothing to propose)"; continue
  fi
  git -C "$work" -c user.name="Buggies Demo" -c user.email="demo@example.com" \
      commit -q -m "proposed change"
  git -C "$work" push -q -f origin "$PR_BRANCH"

  # Open the PR if one is not already open for this branch.
  if gh pr view "$PR_BRANCH" --repo "$repo" >/dev/null 2>&1; then
    echo "    PR already open: $(gh pr view "$PR_BRANCH" --repo "$repo" --json url -q .url)"
  else
    url="$(gh pr create --repo "$repo" --base main --head "$PR_BRANCH" \
            --title "Proposed change" \
            --body "$desc"$'\n\n_Seeded demo PR. Buggies reviews this automatically once the App is installed on the repo._')"
    echo "    opened $url"
  fi
done

echo
echo "Done. Install the Buggies App on these repos (or org-wide on $OWNER),"
echo "then watch the 'buggies' check run and review comments appear on each PR."
