#!/usr/bin/env bash
# capture-dashboard.sh — bring the admin dashboard up locally so it can be
# screenshotted/recorded for the README (the #3 "visual" avenue).
#
# It starts `buggies serve` with a throwaway admin token bound to loopback,
# pointed at a data root of your choosing, and prints the URL + token to paste
# into the dashboard's token gate. Stop it with Ctrl-C.
#
# The dashboard renders whatever is in the stores under $BUGGIES_DATA. To get a
# populated view (findings + proof blocks, not an empty zero-state) either:
#   - point it at a data root from a real run, or
#   - run a review first:  buggies review --repo <dir> ...   (needs the sandbox)
# An empty data root still shows the real UI chrome (Overview/Settings/Budget),
# just with zeroes.
#
# Usage:
#   ./deploy/capture-dashboard.sh                     # data root: ./.buggies-demo
#   BUGGIES_DATA=/path/to/run ./deploy/capture-dashboard.sh
#
# Env:
#   BUGGIES_DATA        data root to serve (default: ./.buggies-demo).
#   BUGGIES_ADMIN_ADDR  loopback listener (default: 127.0.0.1:8081).
#   BUGGIES_BIN         buggies binary (default: ./bin/buggies, else $PATH).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"

bin="${BUGGIES_BIN:-$root/bin/buggies}"
[ -x "$bin" ] || bin="$(command -v buggies || true)"
[ -n "$bin" ] || { echo "no buggies binary; build with: GOWORK=off GOFLAGS=-mod=mod go build -o bin/buggies ./cmd/buggies" >&2; exit 1; }

export BUGGIES_DATA="${BUGGIES_DATA:-$root/.buggies-demo}"
export BUGGIES_ADMIN_ADDR="${BUGGIES_ADMIN_ADDR:-127.0.0.1:8081}"
export BUGGIES_WEB_DIR="${BUGGIES_WEB_DIR:-$root/web/dist}"
export BUGGIES_ADMIN_TOKEN="$(head -c 18 /dev/urandom | base64 | tr -d '/+=')"
mkdir -p "$BUGGIES_DATA"

[ -f "$BUGGIES_WEB_DIR/index.html" ] || echo "warning: $BUGGIES_WEB_DIR not built (cd web && npm install && npm run build)" >&2

cat <<EOF

  Dashboard:  http://$BUGGIES_ADMIN_ADDR
  Token:      $BUGGIES_ADMIN_TOKEN
  Data root:  $BUGGIES_DATA
  Web assets: $BUGGIES_WEB_DIR

  Open the URL, paste the token into the gate, and capture. Ctrl-C to stop.

EOF

# serve validates App config at boot before it mounts the admin surface, but the
# key is only *parsed* when a webhook job runs — and none arrives during a
# screenshot. So dummy values are enough to bring the dashboard up without any
# real credentials or public exposure (the webhook listener binds loopback here).
export BUGGIES_WEBHOOK_SECRET="${BUGGIES_WEBHOOK_SECRET:-screenshot-only}"
export BUGGIES_GITHUB_APP_ID="${BUGGIES_GITHUB_APP_ID:-1}"
if [ -z "${BUGGIES_GITHUB_APP_KEY:-}" ]; then
  keyfile="$BUGGIES_DATA/dummy-app-key.pem"
  [ -f "$keyfile" ] || printf 'screenshot-only placeholder (never used — no webhooks arrive)\n' > "$keyfile"
  export BUGGIES_GITHUB_APP_KEY="$keyfile"
fi

# The webhook listener must bind too, even though no webhooks arrive during a
# screenshot. Keep it on loopback; override BUGGIES_ADDR if the port is taken.
export BUGGIES_ADDR="${BUGGIES_ADDR:-127.0.0.1:8080}"
exec "$bin" serve --addr "$BUGGIES_ADDR"
