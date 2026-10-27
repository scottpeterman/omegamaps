#!/usr/bin/env bash
# scripts/demo-screenshots.sh
#
# README screenshots from the company-free demo network: crawls it with
# cmd/demonet, then replays that crawl through the application window and
# opens its map in the viewer (tests/demo_shots). Nothing from a real network
# is involved, and nothing of yours is read or written -- settings and the
# vault live in a throwaway directory.
#
#   ./scripts/demo-screenshots.sh                 # into ./demo-shots
#   ./scripts/demo-screenshots.sh shots           # into ./shots
#
# Needs the application built with the viewer (build/tests/demo_shots), so
# run ./scripts/build-app.sh first. Headless on Linux; on a Mac it draws real
# windows for a minute or two -- leave them alone while it runs.

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

OUT="${1:-demo-shots}"
BUILD_DIR="${BUILD_DIR:-build}"
SHOTS="${BUILD_DIR}/tests/demo_shots"

die() { echo "error: $*" >&2; exit 1; }
say() { echo "==> $*"; }

[[ -x "${SHOTS}" ]] || die "no ${SHOTS}; build first (./scripts/build-app.sh) with Qt WebEngine available"

mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"

# The crawl. Real-world pacing, so the recording has the rhythm of a real run
# and the mid-crawl grab catches devices in flight; about 20 seconds.
say "crawling the demo network"
go run ./cmd/demonet -out "${OUT}" -name demo -speed 1

# The grabs. Headless where there is no display to draw on.
if [[ "$(uname -s)" != "Darwin" && -z "${DISPLAY:-}" && -z "${WAYLAND_DISPLAY:-}" ]]; then
    export QT_QPA_PLATFORM=offscreen
fi
# Chromium refuses to run as root with its sandbox on.
[[ "$(id -u)" -eq 0 ]] && export QTWEBENGINE_DISABLE_SANDBOX=1

say "taking the screenshots"
set +e
"${SHOTS}" "${OUT}/demo.events.jsonl" "${OUT}/demo.json" "${OUT}" 4 2>&1 | grep -E '^(ok|FAIL)|shots'
rc=${PIPESTATUS[0]}
set -e
[[ "${rc}" -eq 0 ]] || die "some screenshots failed (see FAIL lines above)"

ls -1 "${OUT}"/*.png "${OUT}"/*.drawio 2>/dev/null | sed 's/^/    /'
