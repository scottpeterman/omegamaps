#!/usr/bin/env bash
# scripts/build-app.sh
#
# Builds the Qt application: finds Qt, configures, builds, and says which Qt
# the result actually runs against.
#
#   ./scripts/build-app.sh                         # find Qt, build into build/
#   ./scripts/build-app.sh --qt ~/Qt/6.8.3/gcc_64  # use this Qt
#   ./scripts/build-app.sh --probe events.json [map.json]   # then run replay_probe
#   ./scripts/build-app.sh --clean                 # drop the CMake cache first
#   ./scripts/build-app.sh --list                  # show every Qt found, build nothing
#
# Qt is looked for in this order; the first with Widgets AND Svg wins:
#
#   1. --qt <prefix>
#   2. $OMEGAMAPS_QT, then each entry of $CMAKE_PREFIX_PATH
#   3. ~/Qt/<version>/{gcc_64,gcc_arm64,macos} and /opt/Qt likewise, newest first
#   4. qmake6 / qtpaths6 / qmake on PATH
#   5. the distribution's Qt under /usr
#
# WHY THIS EXISTS. CMake remembers the Qt it found -- or that it found none --
# in the build directory's cache, and a later configure with a corrected
# CMAKE_PREFIX_PATH does not look again. Worse, the cache holds one entry per
# Qt module, so a directory first configured against one Qt and then pointed
# at another links a mix of both. That is the "it never finds Qt" experience,
# and why a fresh build directory always seemed to fix it. This script passes
# the Qt it chose explicitly and drops the cache when the cache disagrees.

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

QT_PREFIX=""
BUILD_DIR="build"
BUILD_TYPE="Release"
CLEAN=0
LIST=0
PROBE_EVENTS=""
PROBE_MAP="-"

usage() { sed -n '3,19p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }
die() { echo "error: $*" >&2; exit 1; }
say() { echo "==> $*"; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --qt) [[ $# -ge 2 ]] || die "--qt needs a path"; QT_PREFIX="$2"; shift 2 ;;
        --qt=*) QT_PREFIX="${1#*=}"; shift ;;
        --build-dir) [[ $# -ge 2 ]] || die "--build-dir needs a path"; BUILD_DIR="$2"; shift 2 ;;
        --debug) BUILD_TYPE="Debug"; shift ;;
        --clean) CLEAN=1; shift ;;
        --list) LIST=1; shift ;;
        --probe)
            [[ $# -ge 2 ]] || die "--probe needs a recording"
            PROBE_EVENTS="$2"; shift 2
            if [[ $# -gt 0 && "$1" != --* ]]; then PROBE_MAP="$1"; shift; fi ;;
        -h|--help) usage 0 ;;
        *) echo "unknown option: $1" >&2; usage 2 ;;
    esac
done

# --- Qt discovery ------------------------------------------------------------

# The directory holding Qt6/Qt6Config.cmake under a prefix. Installer and
# aqtinstall trees use lib/cmake; Debian and Ubuntu put it under the
# multiarch directory; some distributions use lib64.
qt_cmake_dir() {
    local p="$1" d
    for d in "$p/lib/cmake" "$p"/lib/*-linux-gnu*/cmake "$p/lib64/cmake"; do
        [[ -f "$d/Qt6/Qt6Config.cmake" ]] && { echo "$d"; return 0; }
    done
    return 1
}

# Newer Qt keeps the number in Qt6ConfigVersionImpl.cmake and has
# Qt6ConfigVersion.cmake include it; older Qt has it in the latter directly.
qt_version() {
    sed -n 's/^[[:space:]]*set(PACKAGE_VERSION "\([0-9][0-9.]*\)").*/\1/p' \
        "$1/Qt6/Qt6ConfigVersionImpl.cmake" "$1/Qt6/Qt6ConfigVersion.cmake" 2>/dev/null | head -1
}

# 0 if the prefix is a usable Qt: 6.2 or newer, with Widgets and Svg.
# Otherwise prints why on stdout, for the candidate listing.
qt_check() {
    local p="$1" d v
    d="$(qt_cmake_dir "$p")" || { echo "no Qt 6 CMake files"; return 1; }
    v="$(qt_version "$d")"
    [[ -n "$v" ]] || { echo "unreadable version"; return 1; }
    if [[ "$(printf '%s\n6.2\n' "$v" | sort -V | head -1)" != "6.2" ]]; then
        echo "Qt $v is older than 6.2"; return 1
    fi
    [[ -f "$d/Qt6Widgets/Qt6WidgetsConfig.cmake" ]] || { echo "Qt $v has no Widgets"; return 1; }
    [[ -f "$d/Qt6Svg/Qt6SvgConfig.cmake" ]] || { echo "Qt $v has no Svg module"; return 1; }
    # WebEngine is optional: without it everything builds but the map viewer.
    local missing
    missing="$(webengine_missing "$d")"
    if [[ -z "${missing}" ]]; then
        echo "Qt $v"
    elif [[ " ${missing} " == *" Qt6WebEngineWidgets "* ]]; then
        echo "Qt $v (no WebEngine: no map viewer)"
    else
        echo "Qt $v (WebEngine without ${missing}: no map viewer)"
    fi
}

# The modules the map viewer needs that a Qt's CMake directory lacks, by
# config file. WebEngine is not self-contained: WebEngineWidgets needs Quick
# and QuickWidgets, WebEngineCore needs Quick, WebChannel and Positioning.
# Quick is qtdeclarative, which is one of base Qt's archives, not a module --
# so an aqt install made with --archives qtbase has WebEngine's files and
# still cannot build against them.
webengine_missing() {
    local d="$1" m out=()
    for m in WebEngineWidgets WebEngineCore WebChannel Positioning Quick QuickWidgets Qml PrintSupport; do
        [[ -f "$d/Qt6$m/Qt6${m}Config.cmake" ]] || out+=("Qt6$m")
    done
    echo "${out[*]+"${out[*]}"}"
}

candidates() {
    local p
    [[ -n "${OMEGAMAPS_QT:-}" ]] && echo "${OMEGAMAPS_QT}"
    if [[ -n "${CMAKE_PREFIX_PATH:-}" ]]; then
        tr ':;' '\n\n' <<<"${CMAKE_PREFIX_PATH}"
    fi
    for base in "${HOME}/Qt" /opt/Qt; do
        [[ -d "$base" ]] || continue
        # Newest version first; an installer tree also holds Tools/, Docs/
        # and friends, which the version pattern skips.
        for p in $(ls -d "$base"/6.*/ 2>/dev/null | sort -V -r); do
            for kit in gcc_64 gcc_arm64 macos; do
                [[ -d "${p}${kit}" ]] && echo "${p}${kit}"
            done
        done
    done
    for tool in qmake6 qtpaths6 qmake; do
        command -v "$tool" >/dev/null 2>&1 || continue
        if [[ "$tool" == qtpaths6 ]]; then
            "$tool" --query QT_INSTALL_PREFIX 2>/dev/null || true
        else
            "$tool" -query QT_INSTALL_PREFIX 2>/dev/null || true
        fi
    done
    echo /usr
}

if [[ "${LIST}" -eq 1 ]]; then
    say "Qt installations, in the order they are tried"
    candidates | awk 'NF && !seen[$0]++' | while read -r p; do
        printf '    %-50s %s\n' "$p" "$(qt_check "$p" || true)"
    done
    exit 0
fi

if [[ -n "${QT_PREFIX}" ]]; then
    # An explicit path that is wrong is worth stopping for; say what is wrong.
    QT_PREFIX="$(cd "${QT_PREFIX}" 2>/dev/null && pwd)" || die "--qt: no such directory"
    why="$(qt_check "${QT_PREFIX}")" || die "--qt ${QT_PREFIX}: ${why}"
else
    rejected=()
    while read -r p; do
        [[ -d "$p" ]] || continue
        if why="$(qt_check "$p")"; then
            QT_PREFIX="$(cd "$p" && pwd)"
            break
        fi
        rejected+=("$p: ${why}")
    done < <(candidates | awk 'NF && !seen[$0]++')

    if [[ -z "${QT_PREFIX}" ]]; then
        echo "error: no usable Qt found (needs 6.2+ with Widgets and Svg)" >&2
        for r in "${rejected[@]+"${rejected[@]}"}"; do echo "    $r" >&2; done
        cat >&2 <<'HELP'

  Point at one:    ./scripts/build-app.sh --qt ~/Qt/6.8.3/gcc_64
  Ubuntu/Debian:   sudo apt install qt6-base-dev qt6-svg-dev
  aqtinstall:      aqt install-qt linux desktop 6.8.3 linux_gcc_64
HELP
        exit 1
    fi
fi

QT_CMAKE_DIR="$(qt_cmake_dir "${QT_PREFIX}")"
QT_VERSION="$(qt_version "${QT_CMAKE_DIR}")"
say "Qt ${QT_VERSION} at ${QT_PREFIX}"

# --- preflight ---------------------------------------------------------------

command -v go >/dev/null || die "go is not on PATH"
command -v cmake >/dev/null || die "cmake is not on PATH"
CC_BIN="$(go env CC)"
command -v "${CC_BIN}" >/dev/null || die "cgo's C compiler (${CC_BIN}) is not on PATH; the Go archive needs it"

# A go.work above the repo replaces the module set, and the archive step then
# fails with a message about module paths that reads like a broken checkout.
WORKFILE="$(go env GOWORK 2>/dev/null || true)"
if [[ -n "${WORKFILE}" && "${WORKFILE}" != "off" ]] && ! go list ./internal/crawlrun >/dev/null 2>&1; then
    die "a Go workspace is shadowing this module: ${WORKFILE}
  Add this repo to it, or build with: GOWORK=off ./scripts/build-app.sh"
fi

say "toolchain: $(go version | awk '{print $3}'), cmake $(cmake --version | head -1 | awk '{print $3}')"

# --- the cache ---------------------------------------------------------------
# Kept only if it was configured against the Qt chosen now. build/bin, from
# scripts/build.sh, lives in the same directory and is left alone.

CACHE="${BUILD_DIR}/CMakeCache.txt"
drop_cache() {
    rm -f "${CACHE}"
    rm -rf "${BUILD_DIR}/CMakeFiles"
}
if [[ "${CLEAN}" -eq 1 && -f "${CACHE}" ]]; then
    say "dropping the CMake cache (--clean)"
    drop_cache
elif [[ -f "${CACHE}" ]]; then
    # Every Qt6*_DIR entry, not just Qt6_DIR: the cache holds one per module,
    # and passing -DQt6_DIR below overrides only that one. A Widgets or Svg
    # entry left over from another Qt is how a build links two of them.
    want="$(cd "${QT_CMAKE_DIR}" && pwd -P)"
    # When the Qt is Homebrew's, its modules live in per-formula kegs under
    # the same prefix; anything under it is this Qt.
    also=""
    if [[ "$(uname -s)" == "Darwin" ]] && command -v brew >/dev/null 2>&1; then
        bp="$(brew --prefix 2>/dev/null || true)"
        [[ -n "${bp}" && "${QT_PREFIX}" == "${bp}"* ]] && also="$(cd "${bp}" && pwd -P)"
    fi
    stale=""
    while IFS='=' read -r key value; do
        name="${key%%:*}"
        if [[ -z "${value}" || "${value}" == *NOTFOUND* ]]; then
            # Only a required module missing means the configure found no
            # Qt. Optional ones -- WebEngine on a Qt without it, the private
            # Qml pieces WebEngine probes for -- are NOTFOUND in a good cache,
            # and treating them as stale reconfigured on every run.
            case "${name}" in
                Qt6_DIR|Qt6Core_DIR|Qt6Gui_DIR|Qt6Widgets_DIR|Qt6Svg_DIR)
                    stale="${name} recorded no Qt"; break ;;
            esac
            continue
        fi
        # Inside the chosen Qt as written, or once symlinks are resolved.
        # Homebrew needs the first: its lib/cmake/Qt6* entries are symlinks
        # into each formula's own Cellar keg, so every one resolves outside
        # lib/cmake and a resolved-only check dropped the cache on every run.
        # An installer tree reached through a symlink needs the second.
        real="$(cd "${value}" 2>/dev/null && pwd -P || echo "${value}")"
        if [[ "${value}" != "${QT_CMAKE_DIR}"/* && "${real}" != "${want}"/* &&
              ( -z "${also}" || "${real}" != "${also}"/* ) ]]; then
            stale="${name} points at ${value}"; break
        fi
    done < <(grep -E '^Qt6[A-Za-z]*_DIR:[A-Z]+=' "${CACHE}" || true)
    if [[ -n "${stale}" ]]; then
        say "dropping the CMake cache: ${stale}"
        drop_cache
    fi
fi

# --- configure and build -----------------------------------------------------

say "configuring ${BUILD_DIR} (${BUILD_TYPE})"
cmake -S . -B "${BUILD_DIR}" \
      -DCMAKE_BUILD_TYPE="${BUILD_TYPE}" \
      -DCMAKE_PREFIX_PATH="${QT_PREFIX}" \
      -DQt6_DIR="${QT_CMAKE_DIR}/Qt6" >/dev/null

say "building"
cmake --build "${BUILD_DIR}" -j"$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"

APP="${BUILD_DIR}/app/omegamaps"
[[ "$(uname -s)" == "Darwin" ]] && APP="${BUILD_DIR}/app/omegamaps.app/Contents/MacOS/omegamaps"
[[ -x "${APP}" ]] || die "no ${APP} after the build"

# --- which Qt it runs against ------------------------------------------------
# Built against one Qt and loading another at run time -- an LD_LIBRARY_PATH
# left over from something else, most often -- starts fine and fails in odd
# places. Checked here because it is cheap here and confusing anywhere else.

if [[ "$(uname -s)" == "Darwin" ]]; then
    core="$(otool -L "${APP}" | awk '/QtCore/{print $1; exit}')"
else
    core="$(ldd "${APP}" | awk '/libQt6Core/{print $3; exit}')"
fi
if [[ -z "${core}" || "${core}" == "not" ]]; then
    die "${APP} does not resolve QtCore at all (ldd says: ${core:-nothing})"
fi
core_real="$(cd "$(dirname "${core}")" 2>/dev/null && pwd -P)/$(basename "${core}")"
prefix_real="$(cd "${QT_PREFIX}" && pwd -P)"
if [[ "${core_real}" != "${prefix_real}"/* && "${core}" != @rpath/* ]]; then
    echo "warning: built against ${QT_PREFIX} but loads ${core}" >&2
    [[ -n "${LD_LIBRARY_PATH:-}" ]] && echo "         LD_LIBRARY_PATH=${LD_LIBRARY_PATH}" >&2
else
    say "runs against ${core}"
fi

# --- the map viewer ----------------------------------------------------------
# Optional (CMakeLists.txt): built only when this Qt has WebEngine. Said here
# because a CMake warning scrolls away, and "the viewer is not installed" at
# run time is a worse place to learn it.
VIEWER="${BUILD_DIR}/app/omegamaps-viewer"
[[ "$(uname -s)" == "Darwin" ]] && VIEWER="${BUILD_DIR}/app/omegamaps-viewer.app/Contents/MacOS/omegamaps-viewer"
if [[ -x "${VIEWER}" ]]; then
    say "map viewer: ${VIEWER}"
else
    missing="$(webengine_missing "${QT_CMAKE_DIR}")"
    if [[ -z "${missing}" ]]; then
        echo "warning: no map viewer -- Qt ${QT_VERSION} at ${QT_PREFIX} has every module it needs," >&2
        echo "         but CMake did not accept WebEngine: see the CMake warning above for Qt's reason" >&2
    elif [[ " ${missing} " == *" Qt6WebEngineWidgets "* ]]; then
        echo "warning: no map viewer -- Qt ${QT_VERSION} at ${QT_PREFIX} has no WebEngine;" >&2
        echo "         missing: ${missing}" >&2
    else
        echo "warning: no map viewer -- Qt ${QT_VERSION} at ${QT_PREFIX} has WebEngine but not" >&2
        echo "         ${missing}, which it needs" >&2
    fi
    # WebEngine has to come from the same Qt the build uses. A package for
    # the distribution's Qt does nothing for an installer or aqt tree, which
    # is what the first advice here got wrong.
    # Homebrew first: on an Intel Mac its prefix is /usr/local, which is
    # otherwise a distribution's.
    brew_prefix=""
    [[ "$(uname -s)" == "Darwin" ]] && command -v brew >/dev/null 2>&1 && brew_prefix="$(brew --prefix 2>/dev/null || true)"
    case "${QT_PREFIX}" in
        "${brew_prefix:-/nonexistent}"|"${brew_prefix:-/nonexistent}"/*)
            echo "         this is Homebrew's Qt: brew install qtwebengine" >&2 ;;
        /usr|/usr/local)
            echo "         this is the distribution's Qt: install its WebEngine development package" >&2
            echo "         (Ubuntu/Debian: sudo apt install qt6-webengine-dev)" >&2 ;;
        *)
            qt_root="$(cd "${QT_PREFIX}/../.." 2>/dev/null && pwd || true)"
            echo "         add them to this Qt itself; a distribution package will not work with it:" >&2
            # A Maintenance Tool in the root does not mean it installed this
            # Qt: aqt trees often share ~/Qt with an installer one. The
            # installer lists what it manages in components.xml, by component
            # name (qt.qt6.6102.<arch> for 6.10.2); absent there, it is aqt's.
            ver_digits="${QT_VERSION//./}"
            installer_managed=0
            if [[ -n "${qt_root}" && -x "${qt_root}/MaintenanceTool" && -f "${qt_root}/components.xml" ]] &&
               grep -q "qt\.qt6\.${ver_digits}\." "${qt_root}/components.xml"; then
                installer_managed=1
            fi
            if [[ ${installer_managed} -eq 1 ]]; then
                echo "         ${qt_root}/MaintenanceTool -> Qt ${QT_VERSION} -> Additional Libraries ->" >&2
                echo "         Qt WebEngine, Qt WebChannel and Qt Positioning (Qt Quick is in the base install)" >&2
            else
                if [[ -n "${qt_root}" && -x "${qt_root}/MaintenanceTool" ]]; then
                    echo "         (${qt_root}/MaintenanceTool does not manage Qt ${QT_VERSION} -- not in its" >&2
                    echo "         components.xml -- so this is taken to be an aqtinstall tree)" >&2
                fi
                # No Maintenance Tool: an aqtinstall tree. aqt names the host
                # and arch differently from the kit directory, and renamed the
                # Linux arches at Qt 6.7 (gcc_64 -> linux_gcc_64). It does not
                # resolve dependencies, so all of WebEngine's are named: the
                # two modules, and qtdeclarative (Qt Quick), which is a base
                # archive. --archives qtdeclarative installs that one archive
                # of base and nothing else of it; --noarchives skips base
                # entirely, which is right when Quick is already there and
                # wrong for a --archives qtbase tree (omegassh's recipe).
                kit="$(basename "${QT_PREFIX}")"
                new_names=0
                [[ "$(printf '%s\n6.7\n' "${QT_VERSION}" | sort -V | head -1)" == "6.7" ]] && new_names=1
                case "${kit}" in
                    gcc_64)    aqt_host=linux;       aqt_arch=gcc_64;    [[ ${new_names} -eq 1 ]] && aqt_arch=linux_gcc_64 ;;
                    gcc_arm64) aqt_host=linux_arm64; aqt_arch=gcc_arm64; [[ ${new_names} -eq 1 ]] && aqt_arch=linux_gcc_arm64 ;;
                    macos)     aqt_host=mac;         aqt_arch=clang_64 ;;
                    *)         aqt_host="<host>";    aqt_arch="<arch>" ;;
                esac
                # Quick present (a full base): modules only, base untouched.
                # Quick missing (--archives qtbase): its archive as well.
                if [[ " ${missing} " == *" Qt6Quick "* || " ${missing} " == *" Qt6QuickWidgets "* ]]; then
                    base_opt="--archives qtdeclarative"
                    list_what="--archives"; list_note="# has qtdeclarative"
                else
                    base_opt="--noarchives"
                    list_what="--modules"; list_note="# has qtwebengine, qtwebchannel, qtpositioning"
                fi
                # aqt is a pip tool and usually lives in a venv, off PATH
                # (docs/BUILDING.md, "Qt from aqtinstall"). The command shown
                # is one that runs as printed.
                aqt_cmd=""
                if command -v aqt >/dev/null 2>&1; then
                    aqt_cmd=aqt
                else
                    for c in "${OMEGAMAPS_AQT:-}" "${HOME}/venvs/qt/bin/aqt" "${HOME}/.venvs/aqt/bin/aqt" \
                             "${HOME}/.local/bin/aqt"; do
                        [[ -n "$c" && -x "$c" ]] && { aqt_cmd="$c"; break; }
                    done
                fi
                if [[ -z "${aqt_cmd}" ]]; then
                    echo "         aqtinstall is not on PATH or in ~/venvs/qt; set it up once:" >&2
                    echo "           python3 -m venv ~/venvs/qt && ~/venvs/qt/bin/pip install aqtinstall" >&2
                    aqt_cmd="${HOME}/venvs/qt/bin/aqt"
                fi
                aqt_show="${aqt_cmd/#${HOME}/\~}"
                echo "         aqtinstall -- check the names for this release first:" >&2
                echo "           ${aqt_show} list-qt ${aqt_host} desktop ${list_what} ${QT_VERSION} ${aqt_arch}   ${list_note}" >&2
                echo "           ${aqt_show} install-qt ${aqt_host} desktop ${QT_VERSION} ${aqt_arch} ${base_opt} \\" >&2
                echo "               -m qtwebengine qtwebchannel qtpositioning -O ${qt_root:-<Qt root>}" >&2
            fi ;;
    esac
    echo "         then run this script again; the cache does not need clearing" >&2
fi

# --- optional probe ----------------------------------------------------------

if [[ -n "${PROBE_EVENTS}" ]]; then
    [[ -f "${PROBE_EVENTS}" ]] || die "--probe: no such recording: ${PROBE_EVENTS}"
    say "replay_probe"
    QT_QPA_PLATFORM=offscreen "${BUILD_DIR}/tests/replay_probe" \
        "${PROBE_EVENTS}" "${PROBE_MAP}" /tmp/omegamaps-probe 0 2>&1 \
        | grep -vE 'propagateSizeHints|XDG_RUNTIME_DIR'
    [[ "${PIPESTATUS[0]}" -eq 0 ]] || die "replay_probe reported failures"
fi

echo
echo "run it with:"
echo "      ${APP} path/to/events.json"
