#!/usr/bin/env bash
# scripts/build.sh
#
# Full build for Linux and macOS: vet, tests, and the command-line tools.
#
#   ./scripts/build.sh                      # vet, test, build for this machine
#   ./scripts/build.sh --no-test            # build only
#   ./scripts/build.sh --cross              # also every release platform
#   ./scripts/build.sh --version v0.2.0     # stamp this (default: git describe)
#
# Output: build/bin/<tool> for this machine, and with --cross
# build/cross/<os>-<arch>/<tool>[.exe]. build/ is gitignored and is where the
# CMake build goes when the Qt application lands; this script grows a cmake
# stage then, the way omegassh's does.
#
# The SNMP tests that need a live agent run when PFSNMP_TEST_TARGET names one
# (docs/BUILDING.md, "Testing"), and are skipped loudly when it does not.
#
# Tools are built with CGO_ENABLED=0. Nothing in them needs cgo, so --cross
# makes Windows and Linux binaries on a Mac with no other toolchain, and a
# dependency that ever starts to need cgo fails here instead of shipping a
# binary that wants a C runtime on the target.

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

RUN_TESTS=1
CROSS=0
VERSION=""
RELEASE_PLATFORMS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64"
PKG_BUILDINFO="github.com/scottpeterman/omegamaps/internal/buildinfo"

usage() {
    sed -n '3,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-test) RUN_TESTS=0; shift ;;
        --cross) CROSS=1; shift ;;
        --version)
            [[ $# -ge 2 ]] || { echo "error: --version needs a value" >&2; exit 2; }
            VERSION="$2"; shift 2 ;;
        --version=*) VERSION="${1#*=}"; shift ;;
        -h|--help) usage 0 ;;
        *) echo "unknown option: $1" >&2; usage 2 ;;
    esac
done

die() { echo "error: $*" >&2; exit 1; }

# --- preflight -------------------------------------------------------------

command -v go >/dev/null || die "go is not on PATH"
[[ -f go.mod ]] || die "no go.mod at ${ROOT}
  The module file is missing from this working copy. See docs/BUILDING.md."

# A go.work above the repo silently replaces the module set, and every ./...
# then misses -- which reads as a broken tree rather than a stray workspace.
WORKFILE="$(go env GOWORK 2>/dev/null || true)"
if [[ -n "${WORKFILE}" ]] && ! go list ./internal/topo >/dev/null 2>&1; then
    die "a Go workspace is shadowing this module: ${WORKFILE}
  Either add this repo to it, or build with GOWORK=off:

    GOWORK=off ./scripts/build.sh"
fi

# Sandbox mirror replaces are a property of a Claude sandbox, never of the
# project. Building with them is fine; shipping a go.mod that has them is not.
if [[ -d scripts/.floor-backup ]]; then
    echo "==> NOTE: the sandbox floor is applied (scripts/sandbox-floor.sh revert"
    echo "    before delivering anything -- this go.mod is not the shipping one)"
fi

# No go.sum yet: tidy writes it. Except under GOFLAGS=-mod=mod -- a Claude
# sandbox -- where tidy cannot finish (it walks the tests of dependencies into
# hosts the sandbox cannot reach) and the build fills go.sum itself.
if [[ ! -f go.sum ]]; then
    if [[ "$(go env GOFLAGS)" == *-mod=mod* ]]; then
        echo "==> no go.sum; GOFLAGS=-mod=mod, so the build will write it"
    else
        echo "==> go mod tidy (no go.sum yet)"
        go mod tidy
    fi
fi

[[ -n "${VERSION}" ]] || VERSION="$(git describe --tags --always --dirty 2>/dev/null || true)"
LDFLAGS="-s -w"
[[ -n "${VERSION}" ]] && LDFLAGS="${LDFLAGS} -X ${PKG_BUILDINFO}.Version=${VERSION}"

printf '==> toolchain: %s, version %s\n' "$(go version | awk '{print $3}')" "${VERSION:-(unstamped)}"

# --- vet and test ----------------------------------------------------------
# ./... so a package added later is covered the day it lands rather than the
# day someone notices it never ran.

if [[ "${RUN_TESTS}" -eq 1 ]]; then
    echo "==> go vet"
    go vet ./...

    # -race needs cgo and a C compiler. The tools do not; the race build of
    # the tests does. Without a compiler the tests still run, just not raced.
    CC_BIN="$(go env CC 2>/dev/null || true)"
    if [[ "$(go env CGO_ENABLED)" == "1" ]] && command -v "${CC_BIN:-cc}" >/dev/null 2>&1; then
        echo "==> go test (race)"
        go test -race -count=1 ./...
    else
        echo "==> go test (no C compiler for -race; running without it)"
        go test -count=1 ./...
    fi

    if [[ -z "${PFSNMP_TEST_TARGET:-}" ]]; then
        echo "==> SNMP live tests: SKIPPED -- PFSNMP_TEST_TARGET is not set"
        echo "    snmpprobe and the vault-backed SNMP path are unverified against"
        echo "    a real agent in this build. See docs/BUILDING.md, Testing."
    else
        echo "==> SNMP live tests ran against ${PFSNMP_TEST_TARGET}"
    fi
fi

# --- build -----------------------------------------------------------------
# Every cmd/ directory with Go source is a tool, so a new one needs no edit.

TOOLS=""
for d in cmd/*/; do
    ls "${d}"*.go >/dev/null 2>&1 || continue
    t="${d#cmd/}"
    TOOLS="${TOOLS} ${t%/}"
done

build_one() { # goos goarch outdir tool
    local ext=""
    [[ "$1" == "windows" ]] && ext=".exe"
    CGO_ENABLED=0 GOOS="$1" GOARCH="$2" \
        go build -trimpath -ldflags "${LDFLAGS}" -o "$3/$4${ext}" "./cmd/$4"
}

echo "==> build ($(go env GOOS)/$(go env GOARCH))"
mkdir -p build/bin
for t in ${TOOLS}; do
    build_one "$(go env GOOS)" "$(go env GOARCH)" build/bin "${t}"
done

if [[ "${CROSS}" -eq 1 ]]; then
    for plat in ${RELEASE_PLATFORMS}; do
        goos="${plat%/*}"; goarch="${plat#*/}"
        echo "==> build (${plat})"
        mkdir -p "build/cross/${goos}-${goarch}"
        for t in ${TOOLS}; do
            build_one "${goos}" "${goarch}" "build/cross/${goos}-${goarch}" "${t}"
        done
    done
fi

# --- report ----------------------------------------------------------------
# -perm -u+x rather than -executable, which is GNU-only.

# Only directories that exist: build/cross is there after --cross and not
# otherwise, and find exits non-zero on a missing path -- which pipefail turns
# into a failed build after everything has in fact succeeded.
ARTIFACT_DIRS="build/bin"
[[ -d build/cross ]] && ARTIFACT_DIRS="${ARTIFACT_DIRS} build/cross"

echo
echo "artifacts:"
find ${ARTIFACT_DIRS} -type f -perm -u+x | sort | while read -r f; do
    printf '  %-44s %6s KB\n' "${f}" "$(( $(wc -c < "${f}") / 1024 ))"
done
echo
./build/bin/crawl -version

echo
echo "next: ./build/bin/omvault init                  # ~/.omegamaps/vault.json"
echo "      ./build/bin/omvault add -name lab-ro -auth snmp-v2c"
echo "      ./build/bin/crawl -vault ~/.omegamaps/vault.json -methods snmp,ssh \\"
echo "          -seed <switch> -depth 1 -v"
