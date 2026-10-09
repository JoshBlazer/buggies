# Operating `buggies serve`

`buggies serve` runs Buggies unattended: it receives GitHub pull-request
webhooks, reviews each new PR head exactly once, and posts a review plus a CI
check run. This is the operator's guide to standing it up against a live GitHub
App. For what Buggies *is* and how it reviews, see `README.md` and `VISION.md`;
for the internal milestones this assembles, see `ROADMAP.md` (M6–M12 + the
`buggies serve` capstone).

The pipeline is: **webhook → durable queue → worker → checkout PR head →
review → review comment + check run**, gated by a per-repo spend budget, with
fork PRs reviewed with Buggies' secrets stripped from the model subprocess.

---

## 1. Prerequisites (the host)

- **Linux host with a container runtime.** The sandbox runs reproductions in
  Docker, preferably under gVisor (`runsc`). Install Docker and gVisor; confirm
  `docker info` works for the service user. For a dev box without gVisor you may
  set `BUGGIES_ALLOW_RUNC=1` to fall back to `runc` — **never in production**:
  it removes the gVisor isolation boundary around untrusted PR code.
- **The `buggies` binary.** `go build -o buggies ./cmd/buggies` (Go 1.27+), or
  ship the binary you built. `git` must be on `PATH` (used to fetch PR heads).
- **A model backend** (one of):
  - *On-device Claude* (default, no API key): the serving user must have an
    authenticated `claude` CLI on `PATH` (or set `BUGGIES_CLAUDE_BIN`). Works
    headless only if that login is non-interactive on the host. This is how we
    run today.
  - *Anthropic API*: set `BUGGIES_MODEL_BACKEND=anthropic` and provide
    `ANTHROPIC_API_KEY` (or an `ant auth login` profile). Preferred for a
    truly unattended server where no interactive `claude` login exists.
- **A public HTTPS endpoint.** GitHub must reach the webhook. Terminate TLS at a
  reverse proxy (nginx/Caddy) in front of the daemon; `serve` speaks plain HTTP
  on its `--addr` and does not terminate TLS itself.

---

## 2. Create the GitHub App

Settings → Developer settings → **GitHub Apps → New GitHub App**.

- **Webhook URL:** `https://<your-host>/webhook`
- **Webhook secret:** a long random string. This becomes
  `BUGGIES_WEBHOOK_SECRET`. GitHub signs every delivery with it (HMAC-SHA256);
  `serve` rejects any delivery that does not verify (401).
- **Repository permissions** (least privilege — only these):
  | Permission | Access | Why |
  |---|---|---|
  | Contents | Read | fetch the PR head to review it |
  | Pull requests | Read & write | post the review and inline comments |
  | Checks | Read & write | post / update the CI check run |
  | Metadata | Read | mandatory baseline |
- **Subscribe to events:** **Pull requests** only. (`serve` acts on `opened`,
  `reopened`, `synchronize`, and `ready_for_review`; everything else is
  acknowledged and ignored.)
- **Where can this App be installed:** your account/org.

After creating it:
1. Note the **App ID** → `BUGGIES_GITHUB_APP_ID`.
2. **Generate a private key** → download the `.pem`. Store it readable only by
   the service user → path becomes `BUGGIES_GITHUB_APP_KEY`.

---

## 3. Install the App

Install the App on the repositories (or org) you want reviewed. Each
installation gets its own ID; `serve` reads it from each webhook payload and
mints a short-lived installation token per job, so one daemon serves every
installation without per-repo configuration.

---

## 4. Configure and run

Environment:

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `BUGGIES_WEBHOOK_SECRET` | yes | — | the App's webhook secret |
| `BUGGIES_GITHUB_APP_ID` | yes | — | the App's numeric id |
| `BUGGIES_GITHUB_APP_KEY` | yes | — | path to the App private-key PEM |
| `BUGGIES_GITHUB_HOST` | no | `github.com` | set to your GHE host for Enterprise |
| `BUGGIES_BUDGET_CENTS` | no | `0` (no cap) | per-repo monthly spend cap, in cents |
| `BUGGIES_DATA` | no | `~/.buggies` | data root (queue, budget, per-repo state) |
| `BUGGIES_MODEL_BACKEND` | no | `claude-cli` | `claude-cli` (on-device) or `anthropic` |
| `BUGGIES_SKILLS` | no | on | set to `off` to disable the agent skill library |
| `BUGGIES_ALLOW_RUNC` | no | off | dev only: run the sandbox on `runc` |

Flags: `--addr` (default `:8080`, or `BUGGIES_ADDR`), `--interval` (worker poll
interval, default `15s`).

Run:

```sh
export BUGGIES_WEBHOOK_SECRET=…        # same string you gave GitHub
export BUGGIES_GITHUB_APP_ID=123456
export BUGGIES_GITHUB_APP_KEY=/etc/buggies/app.pem
export BUGGIES_BUDGET_CENTS=2000       # e.g. $20/repo/month; omit for no cap
buggies serve --addr :8080
```

Point your reverse proxy's `https://<host>/webhook` at `http://127.0.0.1:8080/webhook`.
A sample systemd unit lives in `deploy/buggies.service` (adjust paths/user).

---

## 5. Verify it works

1. **Liveness:** `curl -fsS https://<host>/healthz` → `ok`.
2. **Delivery:** open (or push to) a PR on an installed repo. In the App's
   **Advanced → Recent Deliveries** you should see the `pull_request` event with
   a `200`. The daemon logs `reviewed <repo> #<n> @ <sha>: N finding(s)` and
   then `posted <repo> #<n>: <url>`.
3. **Result on the PR:** a `buggies` check run, and a review comment per
   reproduced finding (none is the intended outcome for most PRs). The check run
   **fails** only on a reproduced × (data-loss | exploitable) finding; everything
   else is advisory and never blocks the merge.
4. **Idempotency:** GitHub redelivering the same push (same head SHA) does *not*
   produce a second review or comment — the queue dedups on `repo@head_sha`.

---

## 6. Behaviour to know

- **One review per head.** A new push (new head SHA) is a new review; a
  redelivery or a retry of the same head is not. State is the durable SQLite
  queue at `$BUGGIES_DATA/queue.db`, so a crash mid-review loses nothing — the
  job stays claimable and runs on restart.
- **Fork PRs.** The head is fetched via `refs/pull/<N>/head` from the *base*
  repository, so the fork's clone URL and credentials are never used. The model
  subprocess for a fork review runs with every secret
  (`*TOKEN*`/`*SECRET*`/`*KEY*`/…, plus `GITHUB_TOKEN`, `ANTHROPIC_API_KEY`,
  `BUGGIES_STORE_KEY`) stripped from its environment. All PR-derived text (diff,
  filenames, commit messages) is passed to the agents inside a nonce-delimited
  data envelope — it is data, never instructions.
- **Budget.** With `BUGGIES_BUDGET_CENTS` set, a repo over its monthly cap is
  **skipped, not silently dropped**: the daemon posts a neutral check run whose
  summary says why. Spend is recorded per on-device review in
  `$BUGGIES_DATA/budget.db`; resets each calendar month (UTC).
- **Data layout** (all under `$BUGGIES_DATA`, default `~/.buggies`):
  - `queue.db`, `budget.db` — server-wide.
  - `<owner>__<repo>/` — per repo: `graph.db` (code graph, self-indexed on first
    review), `findings.db` (finding store + calibration), `deps/` (sandbox
    dependency cache), `ws/` (sandbox workspaces), `checkout/<head_sha>/`
    (materialised PR trees), `model-cache/` (router fingerprint cache).

---

## 7. Troubleshooting

- **401 in Recent Deliveries:** `BUGGIES_WEBHOOK_SECRET` does not match the App's
  webhook secret.
- **`installation token` errors in the log:** wrong App ID, or a bad PEM
  (wrong path or unreadable by the service user), or the App is not
  installed on that repo.
- **`checkout: head is X, expected Y`:** the PR ref moved between the webhook and
  the fetch; the next `synchronize` delivery carries the new head and reviews it.
- **Reviews post but find nothing on a repo you expect findings on:** confirm the
  sandbox runtime is healthy (`docker info`; gVisor installed) — a review with a
  broken sandbox reproduces nothing, and Buggies only reports what it reproduces.
- **No `claude` on the server (on-device backend):** switch to
  `BUGGIES_MODEL_BACKEND=anthropic` with `ANTHROPIC_API_KEY`.

---

## 8. Security posture (the short version)

Everything in a pull request is **untrusted**: text in diffs, comments, file
names and commit messages is data, never instructions to the agents. Pull
requests from forks never get access to secrets. Reproductions run in the
sandbox, not on the host. These are invariants, not options — see
`ARCHITECTURE.md` for how the sandbox, the untrusted-input envelope, and the
fork-safe environment enforce them.
