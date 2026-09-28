#!/usr/bin/env bash
# scripts/bundle-linux.sh
#
# A self-contained Linux folder for a Qt application: its executables, the Qt
# libraries and plugins they need, and a launcher each, as a .tar.gz.
#
# Every application found goes in one archive, sharing one lib/ and plugins/.
# A GUI that launches a helper beside itself (omegamaps and its map viewer)
# only works that way, and shipping the halves separately gives somebody an
# application whose helper is missing. --separate is the other way, one archive
# per application, for programs that really are independent.
#
#   scripts/bundle-linux.sh                          find the binary, name from the module
#   scripts/bundle-linux.sh --binary build/cmake/app/omegacat
#   scripts/bundle-linux.sh --version v0.1.1 --name omegacat --out dist
#
# Output: dist/<name>-<version>-linux-<arch>.tar.gz, and a line in
# dist/SHA256SUMS-<name>-<version>.txt.
#
# Extract it anywhere and run <name>/<name>. No install, no root, no Qt on the
# machine: the launcher points Qt at the bundled libraries and plugins.
#
# WHAT IS BUNDLED, AND WHAT IS NOT
#
# Qt and the other libraries the binary was built against are copied in --
# they are the ones that have to match. The graphics and system stack is left
# to the machine: glibc, libstdc++, libGL, X11 and xcb, wayland, D-Bus. A
# bundled libGL or libX11 is how a bundle breaks on a machine whose drivers or
# display server are not the ones it was built beside, and those libraries are
# on every desktop already. This is the same split linuxdeploy's exclude list
# makes.
#
# The Qt platform plugin is the part people forget: without plugins/platforms
# the application exits with "could not load the Qt platform plugin xcb".
# Plugins are copied with their own dependencies resolved the same way.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
ROOT="$(pwd)"

BINARY=""
NAME=""
VERSION=""
OUT="dist"
EXTRA_PLUGINS=()
SEPARATE=0

die() { echo "error: $*" >&2; exit 1; }
say() { echo "==> $*"; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --binary) [[ $# -ge 2 ]] || die "--binary needs a path"; BINARY="$2"; shift 2 ;;
        --binary=*) BINARY="${1#*=}"; shift ;;
        --name) [[ $# -ge 2 ]] || die "--name needs a value"; NAME="$2"; shift 2 ;;
        --name=*) NAME="${1#*=}"; shift ;;
        --version) [[ $# -ge 2 ]] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
        --version=*) VERSION="${1#*=}"; shift ;;
        --out) [[ $# -ge 2 ]] || die "--out needs a path"; OUT="$2"; shift 2 ;;
        --out=*) OUT="${1#*=}"; shift ;;
        --plugin) [[ $# -ge 2 ]] || die "--plugin needs a Qt plugin directory name"; EXTRA_PLUGINS+=("$2"); shift 2 ;;
        --separate) SEPARATE=1; shift ;;
        -h|--help) sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
done

[[ "$(uname -s)" == "Linux" ]] || die "this bundles for Linux and has to run on Linux (see bundle-macos.sh)"

# --- what to bundle ----------------------------------------------------------

# Every application, not the first one found: a repository with two of them
# (a tool and its viewer, say) would otherwise ship one and silently skip the
# other, and which one depends on alphabetical order.
BINARIES=()
if [[ -n "${BINARY}" ]]; then
    [[ -x "${BINARY}" ]] || die "${BINARY} is not executable"
    BINARIES=("$(cd "$(dirname "${BINARY}")" && pwd)/$(basename "${BINARY}")")
else
    for candidate in build/cmake/app/* build/app/* build/*/app/*; do
        [[ -f "${candidate}" && -x "${candidate}" ]] || continue
        file "${candidate}" 2>/dev/null | grep -q ELF || continue
        ldd "${candidate}" 2>/dev/null | grep -q libQt6Core || continue
        full="$(cd "$(dirname "${candidate}")" && pwd)/$(basename "${candidate}")"
        # The globs overlap (build/cmake/app/* is also build/*/app/*), so the
        # same binary can be found twice.
        seen=0
        for have in ${BINARIES[@]+"${BINARIES[@]}"}; do
            [[ "${have}" == "${full}" ]] && seen=1
        done
        [[ "${seen}" -eq 1 ]] || BINARIES+=("${full}")
    done
fi
[[ ${#BINARIES[@]} -gt 0 ]] || die "no Qt application binary found; pass --binary (build the app first)"
if [[ ${#BINARIES[@]} -gt 1 && -n "${NAME}" ]]; then
    die "--name with ${#BINARIES[@]} applications found; bundle them one at a time with --binary"
fi
if [[ -z "${VERSION}" ]]; then
    VERSION="$(git describe --tags --always --dirty 2>/dev/null || true)"
fi
[[ -n "${VERSION}" ]] || VERSION="unstamped"
case "${VERSION}" in
    *-dirty) echo "warning: the tree has uncommitted changes (${VERSION})" >&2 ;;
esac
ARCH="$(uname -m)"

command -v ldd >/dev/null || die "ldd is not on PATH"

mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"
ARCHIVES=()
declare -A RUN_HINT   # archive name -> the program to run from it

# bundle_one NAME BINARY... -- one archive holding every binary given.
bundle_one() {
local NAME="$1"; shift
local BINARIES_IN=("$@")
local BINARY="${BINARIES_IN[0]}"

# The Qt this binary was built against, from its own libQt6Core.
QT_CORE="$(ldd "${BINARY}" | awk '/libQt6Core\.so/ {print $3}' | head -1)"
[[ -n "${QT_CORE}" && -f "${QT_CORE}" ]] || die "the binary does not link Qt6 Core; is ${BINARY} the application?"
QT_LIB_DIR="$(dirname "${QT_CORE}")"

# Qt's plugin directory: ask qmake/qtpaths where they exist, else the usual
# places beside the libraries.
QT_PLUGIN_DIR=""
for q in qtpaths6 qtpaths qmake6 qmake; do
    command -v "$q" >/dev/null || continue
    case "$q" in
        qtpaths*) QT_PLUGIN_DIR="$("$q" --query QT_INSTALL_PLUGINS 2>/dev/null || true)" ;;
        qmake*)   QT_PLUGIN_DIR="$("$q" -query QT_INSTALL_PLUGINS 2>/dev/null || true)" ;;
    esac
    [[ -n "${QT_PLUGIN_DIR}" && -d "${QT_PLUGIN_DIR}" ]] && break
    QT_PLUGIN_DIR=""
done
if [[ -z "${QT_PLUGIN_DIR}" ]]; then
    for candidate in "${QT_LIB_DIR}/qt6/plugins" "${QT_LIB_DIR}/../plugins" "${QT_LIB_DIR}/qt6/plugins"; do
        [[ -d "${candidate}" ]] && { QT_PLUGIN_DIR="$(cd "${candidate}" && pwd)"; break; }
    done
fi
[[ -n "${QT_PLUGIN_DIR}" ]] || die "cannot find Qt's plugin directory (install qtpaths, or point QT_PLUGIN_DIR at it)"

# Left to the machine. Anything matching these is not copied: the graphics,
# display and system stack has to be the host's.
is_system_lib() {
    local base="$1"
    case "${base}" in
        ld-linux*|libc.so.*|libm.so.*|libdl.so.*|libpthread.so.*|librt.so.*|libutil.so.*) return 0 ;;
        libstdc++.so.*|libgcc_s.so.*|libatomic.so.*) return 0 ;;
        libGL.so.*|libGLX.so.*|libGLdispatch.so.*|libEGL.so.*|libOpenGL.so.*|libGLU.so.*|libdrm.so.*|libgbm.so.*) return 0 ;;
        libX11*.so.*|libXext.so.*|libXrender.so.*|libXi.so.*|libXfixes.so.*|libXcursor.so.*|libXrandr.so.*|libXau.so.*|libXdmcp.so.*|libXxf86vm.so.*|libSM.so.*|libICE.so.*) return 0 ;;
        libxcb*.so.*) return 0 ;;
        libwayland*.so.*) return 0 ;;
        libdbus-1.so.*) return 0 ;;
        libudev.so.*|libsystemd.so.*|libselinux.so.*|libcap.so.*) return 0 ;;
        libasound.so.*|libpulse*.so.*) return 0 ;;
        libz.so.*|libresolv.so.*|libnsl.so.*) return 0 ;;
    esac
    return 1
}

local STAGE DIR archive sums sum
STAGE="$(mktemp -d)"
DIR="${STAGE}/${NAME}"
mkdir -p "${DIR}/bin" "${DIR}/lib" "${DIR}/plugins"

for b in "${BINARIES_IN[@]}"; do
    cp "${b}" "${DIR}/bin/$(basename "${b}")"
    chmod +x "${DIR}/bin/$(basename "${b}")"
done

# copy_deps walks ldd for one file and copies what is not the machine's.
copy_deps() {
    local target="$1" line base path
    while read -r line; do
        base="$(awk '{print $1}' <<<"${line}")"
        path="$(awk '{print $3}' <<<"${line}")"
        [[ -n "${path}" && -f "${path}" ]] || continue
        is_system_lib "${base}" && continue
        [[ -f "${DIR}/lib/${base}" ]] && continue
        cp -L "${path}" "${DIR}/lib/${base}"
        copy_deps "${path}"   # a copied library's own dependencies
    done < <(ldd "${target}" 2>/dev/null | sed 's/^[[:space:]]*//')
}

say "bundling ${NAME} ${VERSION} (${ARCH}): $(printf '%s ' "${BINARIES_IN[@]##*/}")"
say "Qt libraries: ${QT_LIB_DIR}"
for b in "${BINARIES_IN[@]}"; do
    copy_deps "${b}"
done

# Plugins: the platform plugin is required; the rest are what a desktop
# application normally reaches for. Missing ones are skipped, not fatal --
# distributions split Qt differently.
plugin_groups=(platforms xcbglintegrations imageformats iconengines platforminputcontexts platformthemes tls)
plugin_groups+=("${EXTRA_PLUGINS[@]+"${EXTRA_PLUGINS[@]}"}")
for group in "${plugin_groups[@]}"; do
    src="${QT_PLUGIN_DIR}/${group}"
    [[ -d "${src}" ]] || continue
    mkdir -p "${DIR}/plugins/${group}"
    for so in "${src}"/*.so; do
        [[ -f "${so}" ]] || continue
        cp -L "${so}" "${DIR}/plugins/${group}/"
        copy_deps "${so}"
    done
done
[[ -f "${DIR}/plugins/platforms/libqxcb.so" ]] || \
    echo "warning: no xcb platform plugin in ${QT_PLUGIN_DIR}/platforms; the application may not start" >&2

# Qt finds its plugins through this, beside the executable.
cat > "${DIR}/bin/qt.conf" <<'EOF'
[Paths]
Prefix = ..
Plugins = plugins
Libraries = lib
EOF

# The launcher: the bundled libraries first, then whatever the machine has.
# A launcher per program, each starting its own executable out of bin/, where
# they sit together: a program that looks for a helper beside itself finds it.
for b in "${BINARIES_IN[@]}"; do
    local prog
    prog="$(basename "${b}")"
    cat > "${DIR}/${prog}" <<EOF
#!/usr/bin/env bash
# Runs the bundled program from wherever this folder was extracted.
set -euo pipefail
here="\$(cd "\$(dirname "\$(readlink -f "\${BASH_SOURCE[0]}")")" && pwd)"
export LD_LIBRARY_PATH="\${here}/lib\${LD_LIBRARY_PATH:+:\${LD_LIBRARY_PATH}}"
export QT_PLUGIN_PATH="\${here}/plugins"
exec "\${here}/bin/${prog}" "\\$@"
EOF
    chmod +x "${DIR}/${prog}"
done

for doc in README.md LICENSE; do
    [[ -f "${doc}" ]] && cp "${doc}" "${DIR}/"
done

archive="${OUT}/${NAME}-${VERSION}-linux-${ARCH}.tar.gz"
rm -f "${archive}"
(cd "${STAGE}" && tar czf "${archive}" "${NAME}")

sums="${OUT}/SHA256SUMS-${NAME}-${VERSION}.txt"
if command -v sha256sum >/dev/null; then sum="$(sha256sum "${archive}" | awk '{print $1}')";
else sum="$(shasum -a 256 "${archive}" | awk '{print $1}')"; fi
touch "${sums}"
grep -v "  $(basename "${archive}")\$" "${sums}" > "${sums}.new" 2>/dev/null || true
mv "${sums}.new" "${sums}" 2>/dev/null || true
echo "${sum}  $(basename "${archive}")" >> "${sums}"

local libs plugins
libs=$(find "${DIR}/lib" -name '*.so*' | wc -l)
plugins=$(find "${DIR}/plugins" -name '*.so' | wc -l)
printf '  %s  %s  (%s libraries, %s plugins)\n' \
    "$(du -h "${archive}" | awk '{print $1}')" "$(basename "${archive}")" "${libs}" "${plugins}"
ARCHIVES+=("${archive}")
RUN_HINT["${NAME}"]="$(basename "${BINARIES_IN[0]}")"
rm -rf "${STAGE}"
}

# The name of one archive holding several programs: the application the others
# hang off, which is the one whose name every other name starts with
# (omegamaps, omegamaps-viewer). With no such name, the module's.
primary_name() {
    local candidate other ok
    for candidate in "${BINARIES[@]##*/}"; do
        ok=1
        for other in "${BINARIES[@]##*/}"; do
            [[ "${other}" == "${candidate}"* ]] || ok=0
        done
        [[ "${ok}" -eq 1 ]] && { echo "${candidate}"; return; }
    done
    echo "$(basename "$(go list -m 2>/dev/null || basename "${ROOT}")")"
}

if [[ "${SEPARATE}" -eq 1 || ${#BINARIES[@]} -eq 1 ]]; then
    for b in "${BINARIES[@]}"; do
        bundle_one "${NAME:-$(basename "${b}")}" "${b}"
    done
else
    bundle_one "${NAME:-$(primary_name)}" "${BINARIES[@]}"
fi

echo
echo "archives (${VERSION}) in ${OUT}:"
for a in "${ARCHIVES[@]}"; do
    app="$(basename "${a%%-${VERSION}-*}")"
    echo "  $(basename "${a}")"
    echo "      tar xzf $(basename "${a}") && ${app}/${RUN_HINT[${app}]:-${app}}"
    echo "      checksums: SHA256SUMS-${app}-${VERSION}.txt"
done