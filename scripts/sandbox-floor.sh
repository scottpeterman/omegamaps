#!/usr/bin/env bash
# scripts/sandbox-floor.sh
#
# Put the working tree on the floor dependency set, for a Claude sandbox that
# cannot reach proxy.golang.org -- and take it off again, exactly.
#
#   scripts/sandbox-floor.sh apply     # back up go.mod/go.sum, apply
#                                      # scripts/floor.deps + scripts/floor.local
#   scripts/sandbox-floor.sh revert    # restore the backup byte for byte
#   scripts/sandbox-floor.sh status
#
# test.sh -F never needs this: it applies the same two files to a throwaway
# copy. This is for what -F cannot do -- building binaries to run, or
# iterating on one package. The backup is what makes the revert exact rather
# than reconstructed from memory, which is how a sandbox go.mod gets shipped.
#
# Build with these set while it is applied:
#   export GOTOOLCHAIN=local GOPROXY=direct GOSUMDB=off GOPRIVATE='*' GOFLAGS=-mod=mod
#
# See docs/README_Claude_sandbox.md.

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
BACKUP="scripts/.floor-backup"

die() { echo "error: $*" >&2; exit 1; }

case "${1:-status}" in
apply)
    [[ ! -d "${BACKUP}" ]] || die "already applied (${BACKUP} exists); revert first"
    [[ -f scripts/floor.deps ]] || die "no scripts/floor.deps"
    [[ -f scripts/floor.local ]] || die "no scripts/floor.local -- its contents are in docs/README_Claude_sandbox.md"
    mkdir -p "${BACKUP}"
    cp go.mod "${BACKUP}/go.mod"
    [[ -f go.sum ]] && cp go.sum "${BACKUP}/go.sum"
    export GOTOOLCHAIN=local GOPROXY=direct GOSUMDB=off GOPRIVATE='*' GOFLAGS=-mod=mod
    while read -r mod ver; do
        case "${mod}" in ""|\#*) continue ;; esac
        if [[ "${mod}" == "go" ]]; then
            go mod edit -go="${ver}"
        else
            go mod edit -require="${mod}@${ver}"
        fi
    done < scripts/floor.deps
    cat scripts/floor.local >> go.mod
    # No go mod tidy: it walks the tests of dependencies, gosnmp's reach
    # gopkg.in/yaml.v3 through testify, and gopkg.in is not on the sandbox
    # allowlist. With GOFLAGS=-mod=mod the build fills go.sum as it goes.
    echo "applied: go.mod is on the floor set and is NOT shippable until: $0 revert"
    ;;
revert)
    [[ -d "${BACKUP}" ]] || die "not applied (no ${BACKUP})"
    cp "${BACKUP}/go.mod" go.mod
    if [[ -f "${BACKUP}/go.sum" ]]; then cp "${BACKUP}/go.sum" go.sum; else rm -f go.sum; fi
    rm -rf "${BACKUP}"
    echo "reverted: go.mod and go.sum are the shipping ones again"
    ;;
status)
    if [[ -d "${BACKUP}" ]]; then
        echo "applied -- revert before delivering anything"
    elif grep -q 'github.com/golang/' go.mod; then
        echo "NOT applied, but go.mod has mirror replaces in it -- it is not the shipping file"
        exit 1
    else
        echo "clean"
    fi
    ;;
*) sed -n '3,22p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
