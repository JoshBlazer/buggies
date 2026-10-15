# Buggies dashboard (`web/`)

The admin dashboard — the fourth surface in ARCHITECTURE.md (PR comment, incident
card, CI gate, **dashboard**). A Vite + React + TypeScript SPA that talks to the
`buggies serve` daemon's JSON admin API under `/api` and builds to static assets
the daemon serves on its **loopback** admin mux.

## Develop

```sh
npm install
npm run dev          # http://localhost:5173, proxies /api to the daemon
BUGGIES_API=http://localhost:8080 npm run dev   # point at a non-default daemon
```

Run the daemon alongside it with an admin token set:
`BUGGIES_ADMIN_TOKEN=… buggies serve`.

## Build

```sh
npm run build        # tsc --noEmit && vite build -> web/dist/
```

`vite.config.ts` sets `base: "./"`, so every asset reference is relative and the
bundle works whether the daemon mounts it at `/` or behind a subpath. Routing is
hash-based (`createHashRouter`), so deep links resolve from `index.html` with no
server-side SPA fallback.

## Auth (fail closed)

The daemon mounts `/api` and this dashboard **only when `BUGGIES_ADMIN_TOKEN`
is set**, bound to `127.0.0.1` (separate from the public `BUGGIES_ADDR` webhook
listener). Every `/api/*` request carries `Authorization: Bearer <token>`,
compared constant-time on the Go side.

`src/components/TokenGate.tsx` holds the SPA behind that token: nothing calls
`/api` until a token is entered. The token is kept in `localStorage` and sent as
a header (never a cookie → no CSRF surface). A `401` from any request clears the
stored token and drops the user back to the gate with a reason.

## Serving from the daemon (Go side — owned by the API work, not this dir)

The daemon serves `web/dist/` as static files on the loopback admin mux and
routes `/api/*` to the JSON API:

```go
//go:embed all:web/dist
var dashboardFS embed.FS

sub, _ := fs.Sub(dashboardFS, "web/dist")
adminMux.Handle("/", http.FileServer(http.FS(sub)))  // static assets + index.html
adminMux.Handle("/api/", requireAdminToken(apiHandler))
```

Because routing is hash-based, no catch-all rewrite to `index.html` is needed.

## API contract

`src/api/types.ts` is the agreed JSON shape. It is **camelCase** and uses the
kernel's string enum spellings (`reproduced`, `data-loss`, …), not the raw Go
integers. The Go side marshals a DTO to this shape rather than `finding.Finding`
directly (whose default `json` is PascalCase with integer confidence/impact).

Secrets (`BUGGIES_WEBHOOK_SECRET`, the GitHub App PEM, `BUGGIES_STORE_KEY`, and
installation tokens) never appear in any response — `/api/settings` reports only
their presence.

Endpoints consumed:

| Method | Path | Returns |
|---|---|---|
| GET | `/api/overview` | `Overview` (queue depth, finding tallies, spend) |
| GET | `/api/jobs?state=&repo=&limit=&offset=` | `JobsPage` |
| GET | `/api/jobs/:id` | `JobDetail` (job + findings + check) |
| POST | `/api/jobs/:id/requeue` | `{id,state}` — 409 if running |
| GET | `/api/repos` | `ReposResponse` |
| GET | `/api/findings?repo=&path=&type=&state=&minConfidence=&limit=` | `FindingsResponse` (repo required) |
| GET | `/api/findings/:id?repo=` | `FindingDetail` (with `proof` + rendered markdown) |
| GET | `/api/findings/:id/history?repo=` | `FindingHistory` |
| POST | `/api/findings/:id/transition` `{repo,to,reason}` | `TransitionResult` |
| GET | `/api/budget` | `Budget` |
| GET | `/api/settings` | `Settings` |
| PUT | `/api/settings` `{budgetCents}` | `{budgetCents}` — 400 on negative |

The write surface is deliberately narrow: finding lifecycle transitions, the
per-repo monthly budget cap, and re-queuing a reviewed PR head. Everything else
is read-only.

## Layout

```
web/
  package.json  index.html  vite.config.ts  tsconfig.json
  src/
    main.tsx            React + React Query bootstrap, wrapped in TokenGate
    App.tsx             hash router + routes
    index.css           dark-first theme, verification/severity color tokens
    api/
      types.ts          the API contract (source of truth for the wire shape)
      auth.ts           admin-token store (bearer header, localStorage)
      client.ts         fetch wrapper (base path, bearer auth, errors, JSON)
      hooks.ts          React Query hooks + mutations
    lib/format.ts       money/time/number/label helpers, ranking
    components/
      Layout.tsx  TokenGate.tsx  Badges.tsx  States.tsx
      StatCard.tsx  ProofBlock.tsx  Meter.tsx
    pages/
      Overview.tsx  PullRequests.tsx  JobDetail.tsx  Findings.tsx
      FindingDetail.tsx  Repos.tsx  Budget.tsx  Settings.tsx
```
