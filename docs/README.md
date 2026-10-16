# docs/ — README assets

Images embedded in the top-level [README](../README.md).

| File | What it is | How it was made |
|------|-----------|-----------------|
| `dashboard-overview.jpg` | The admin dashboard Overview page, showing one reproduced, blocking finding | Captured from the real dashboard serving a data root produced by an actual `buggies review` of the `gold-tier-discount` fixture |

## Regenerating

The dashboard assets come from the real system, not a mockup. To recapture:

1. Produce a data root with at least one finding — either point at a real run,
   or review a fixture locally:
   ```sh
   # throwaway repo from a fixture, then review it (on-device model, runc sandbox)
   BUGGIES_ALLOW_RUNC=1 BUGGIES_CLAUDE_BIN=/path/to/claude \
     BUGGIES_DATA=./.demo-data buggies review --repo <tmp> --name acme/shop --base <sha>
   ```
2. Serve the dashboard against it and screenshot:
   ```sh
   BUGGIES_DATA=./.demo-data ./deploy/capture-dashboard.sh
   # open the printed http://127.0.0.1:8081 URL, paste the token, capture
   ```

The PR-comment example in the README is the finding's `renderedMarkdown` field
from `GET /api/findings/{id}` — the exact body Buggies posts on a pull request.
