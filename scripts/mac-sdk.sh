#!/usr/bin/env bash
# scripts/mac-sdk.sh
#
# Prints the path of a macOS SDK that this machine's compiler can actually
# link against, newest first. For a shell, or for the build scripts:
#
#   export SDKROOT="$(scripts/mac-sdk.sh)"
#   scripts/mac-sdk.sh -v          # say what was tried and why each was skipped
#
# WHY THIS EXISTS. A managed Mac takes Command Line Tools updates on its own
# schedule, and an update can install an SDK the installed linker cannot read
# -- MacOSX27.0.sdk listing an "arm64e.x1" architecture that ld rejects with
# "tapi error: malformed file". The default SDK (xcrun, and the unversioned
# MacOSX.sdk link) then points at the broken one, and every C or C++ build
# fails at CMake's first compiler check. Which SDK works is not something a
# version number says, so this asks the linker: each candidate is used to
# compile and link a C and a C++ program, and the first that links wins.
#
# Exits 1, naming every SDK it tried, when none links.

set -u

VERBOSE=0
[[ "${1:-}" == "-v" || "${1:-}" == "--verbose" ]] && VERBOSE=1

note() { [[ "${VERBOSE}" -eq 1 ]] && echo "mac-sdk: $*" >&2; return 0; }

[[ "$(uname -s)" == "Darwin" ]] || { echo "mac-sdk: not macOS" >&2; exit 1; }

ARCH="$(uname -m)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/mac-sdk.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT

printf 'int main(void) { return 0; }\n' > "${WORK}/t.c"
printf '#include <string>\nint main() { std::string s("x"); return (int)s.size() - 1; }\n' > "${WORK}/t.cpp"

# Compiles and links both test programs against $1. The C++ one matters: it
# pulls in libc++ from the SDK, which the C one never touches.
links() {
    local sdk="$1" err
    if ! err="$(cc -isysroot "${sdk}" -arch "${ARCH}" "${WORK}/t.c" -o "${WORK}/t_c" 2>&1)"; then
        note "skip ${sdk}: C link failed: $(printf '%s' "${err}" | grep -m1 -E 'error' || printf '%s' "${err}" | head -1)"
        return 1
    fi
    if ! err="$(c++ -isysroot "${sdk}" -arch "${ARCH}" "${WORK}/t.cpp" -o "${WORK}/t_cpp" 2>&1)"; then
        note "skip ${sdk}: C++ link failed: $(printf '%s' "${err}" | grep -m1 -E 'error' || printf '%s' "${err}" | head -1)"
        return 1
    fi
    return 0
}

# An SDK already chosen by the caller wins if it works, so an explicit
# SDKROOT is respected rather than second-guessed.
if [[ -n "${SDKROOT:-}" ]]; then
    if [[ -d "${SDKROOT}" ]] && links "${SDKROOT}"; then
        note "using SDKROOT as set: ${SDKROOT}"
        echo "${SDKROOT}"
        exit 0
    fi
    note "SDKROOT=${SDKROOT} does not link; looking for another"
fi

# Every SDK directory there is: the Command Line Tools and any Xcode. The
# unversioned and major-only names (MacOSX.sdk, MacOSX26.sdk) are usually
# symlinks to a versioned one, so each is resolved and listed once, under its
# real path, with its version for sorting.
candidates() {
    local dir sdk real ver
    for dir in /Library/Developer/CommandLineTools/SDKs \
               /Applications/Xcode*.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs; do
        [[ -d "${dir}" ]] || continue
        for sdk in "${dir}"/MacOSX*.sdk; do
            [[ -d "${sdk}" ]] || continue
            real="$(cd -P "${sdk}" 2>/dev/null && pwd)" || continue
            ver="$(basename "${real}" .sdk)"
            ver="${ver#MacOSX}"
            [[ -n "${ver}" ]] || ver="0"
            # "26.5" -> major, minor, patch as separate numeric sort keys.
            printf '%s\t%s\n' "$(printf '%s.0.0' "${ver}" | awk -F. '{print $1"\t"$2"\t"$3}')" "${real}"
        done
    done | sort -t$'\t' -u -k4,4 | sort -t$'\t' -k1,1nr -k2,2nr -k3,3nr | cut -f4
}

# xcrun's own default first, then everything else newest first. The default
# is what an unpinned build would have used; when it works there is no reason
# to pick anything else.
DEFAULT="$(xcrun --sdk macosx --show-sdk-path 2>/dev/null || true)"
[[ -n "${DEFAULT}" ]] && DEFAULT="$(cd -P "${DEFAULT}" 2>/dev/null && pwd || echo "${DEFAULT}")"

LIST="$( { [[ -n "${DEFAULT}" ]] && echo "${DEFAULT}"; candidates; } )"
TRIED=""
while IFS= read -r sdk; do
    [[ -n "${sdk}" ]] || continue
    printf '%s\n' "${TRIED}" | grep -Fxq -- "${sdk}" && continue
    TRIED="${TRIED}${TRIED:+$'\n'}${sdk}"
    if links "${sdk}"; then
        if [[ "${sdk}" == "${DEFAULT}" ]]; then
            note "default SDK links: ${sdk}"
        else
            note "chose ${sdk} (default ${DEFAULT:-none} does not link)"
        fi
        echo "${sdk}"
        exit 0
    fi
done <<< "${LIST}"

echo "mac-sdk: no installed macOS SDK links with this compiler. Tried:" >&2
[[ -n "${TRIED}" ]] && printf '%s\n' "${TRIED}" | sed 's/^/  /' >&2
echo "mac-sdk: reinstall the Command Line Tools: sudo rm -rf /Library/Developer/CommandLineTools && xcode-select --install" >&2
exit 1
