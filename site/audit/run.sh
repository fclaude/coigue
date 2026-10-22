#!/usr/bin/env bash
# Builds the site for a local address, serves it, and runs the accessibility
# checks against it: axe-core on every page in light and dark at 1280 and 320
# pixels, then keyboard navigation and reflow. Needs zola, python3, node, and
# Chromium for Playwright (npx playwright install chromium).
#
#   bash site/audit/run.sh
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
site="$(dirname "$here")"
port="${PORT:-8099}"
out="$(mktemp -d)"
trap 'kill "$server" 2>/dev/null || true; rm -rf "$out"' EXIT

python3 "$site/build.py"
(cd "$site" && zola build --base-url "http://127.0.0.1:$port" -o "$out/site" >/dev/null && python3 finish.py "$out/site")
(cd "$here" && npm install --no-audit --no-fund >/dev/null)
python3 -m http.server "$port" --bind 127.0.0.1 --directory "$out/site" >/dev/null 2>&1 &
server=$!
sleep 1
export BASE="http://127.0.0.1:$port"
(cd "$here" && node axe.js && node keyboard.js)
