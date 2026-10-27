#!/usr/bin/env bash
# scripts/bundle-mac.sh
#
# Packages omegamaps and its map viewer for macOS: both .app bundles made
# self-contained with macdeployqt, signed, and put in one .dmg.
#
#   ./scripts/bundle-mac.sh                     # Qt from PATH (Homebrew or installer)
#   ./scripts/bundle-mac.sh --qt ~/Qt/6.10.3/macos
#   ./scripts/bundle-mac.sh --sign "Developer ID Application: ..."
#   ./scripts/bundle-mac.sh --no-dmg            # stop at the two bundles
#
# TWO BUNDLES, SIDE BY SIDE. The application starts the viewer as the sibling
# bundle omegamaps-viewer.app (app/viewerlaunch.cpp), so the dmg carries both
# and a person drags both to Applications. The viewer carries Qt WebEngine --
# a framework with its own helper application inside -- which is why it is
# deployed as a bundle of its own rather than folded into the first.
#
# THE SDK. The build goes through scripts/mac-sdk.sh, which picks an SDK the
# linker can actually read: a Command Line Tools update can install one it
# cannot ("tapi error: malformed file"). SDKROOT carries it to the Go archive
# step, CMAKE_OSX_SYSROOT to the C++.
#
# Output: dist/omegamaps-<version>-<arch>.dmg (and the bundles in build-mac/app)

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

QT_PREFIX="${OMEGAMAPS_QT:-}"
BUILD_DIR="build-mac"
SIGN_ID=""
MAKE_DMG=1

while [[ $# -gt 0 ]]; do
    case "$1" in
        --qt) QT_PREFIX="$2"; shift 2 ;;
        --qt=*) QT_PREFIX="${1#*=}"; shift ;;
        --sign) SIGN_ID="$2"; shift 2 ;;
        --no-dmg) MAKE_DMG=0; shift ;;
        --build-dir) BUILD_DIR="$2"; shift 2 ;;
        -h|--help) sed -n '2,24p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

die() { echo "error: $*" >&2; exit 1; }
say() { echo "==> $*"; }

[[ "$(uname -s)" == "Darwin" ]] || die "this one is macOS only; see bundle-appimage.sh"

# --- Qt ----------------------------------------------------------------------
if [[ -n "${QT_PREFIX}" ]]; then
    QMAKE="${QT_PREFIX}/bin/qmake"
    [[ -x "${QMAKE}" ]] || QMAKE="${QT_PREFIX}/bin/qmake6"
fi
[[ -n "${QMAKE:-}" ]] || QMAKE="$(command -v qmake6 || command -v qmake || true)"
[[ -n "${QMAKE}" && -x "${QMAKE}" ]] || die "no qmake; pass --qt <prefix> or put Qt's bin on PATH"
QT_PREFIX="$("${QMAKE}" -query QT_INSTALL_PREFIX)"
QT_BINS="$("${QMAKE}" -query QT_INSTALL_BINS)"
MACDEPLOYQT="${QT_BINS}/macdeployqt"
[[ -x "${MACDEPLOYQT}" ]] || MACDEPLOYQT="$(command -v macdeployqt || true)"
[[ -n "${MACDEPLOYQT}" ]] || die "no macdeployqt beside ${QMAKE}"
say "Qt $("${QMAKE}" -query QT_VERSION) at ${QT_PREFIX}"

# --- SDK ---------------------------------------------------------------------
SDKROOT="$("$(dirname "${BASH_SOURCE[0]}")/mac-sdk.sh" -v)" || die "no usable macOS SDK (see above)"
export SDKROOT
say "macOS SDK: ${SDKROOT}"

# --- build -------------------------------------------------------------------
ARCH="$(uname -m)"
say "building into ${BUILD_DIR} (${ARCH})"
cmake -S . -B "${BUILD_DIR}" -DCMAKE_BUILD_TYPE=Release -DCMAKE_PREFIX_PATH="${QT_PREFIX}" \
      -DCMAKE_OSX_SYSROOT="${SDKROOT}" -DCMAKE_OSX_ARCHITECTURES="${ARCH}" >/dev/null
cmake --build "${BUILD_DIR}" --target omegamaps omegamaps-viewer -j"$(sysctl -n hw.ncpu)"

APP="${BUILD_DIR}/app/omegamaps.app"
VIEWER="${BUILD_DIR}/app/omegamaps-viewer.app"
[[ -d "${APP}" ]] || die "no ${APP} after the build"
[[ -d "${VIEWER}" ]] || die "no ${VIEWER}: this Qt has no WebEngine, and the package would ship without its viewer"
VERSION="$(tr -d '[:space:]' < "${BUILD_DIR}/app-version.txt")"
VERSION="${VERSION#v}"

# --- deploy ------------------------------------------------------------------
# macdeployqt is not reliably idempotent -- it rewrites install names in
# place -- so each run starts from a freshly linked binary.
for b in "${APP}" "${VIEWER}"; do
    exe="${b}/Contents/MacOS/$(basename "${b}" .app)"
    rm -f "${exe}"
done
cmake --build "${BUILD_DIR}" --target omegamaps omegamaps-viewer -j"$(sysctl -n hw.ncpu)" >/dev/null

say "macdeployqt: omegamaps.app"
"${MACDEPLOYQT}" "${APP}" -verbose=1
say "macdeployqt: omegamaps-viewer.app (with Qt WebEngine)"
"${MACDEPLOYQT}" "${VIEWER}" -verbose=1

# --- verify ------------------------------------------------------------------
# A Qt reference is self-contained when it is @executable_path/@loader_path,
# or @rpath with the framework actually in Contents/Frameworks. Absolute
# rpaths -- the build machine's Qt -- are removed: harmless here, a trap on any
# other Mac. Signing comes after, so these edits invalidate nothing.
check_bundle() {
    local bundle="$1"
    local bin="${bundle}/Contents/MacOS/$(basename "${bundle}" .app)"
    while read -r rp; do
        [[ -z "${rp}" || "${rp}" == @* ]] && continue
        say "$(basename "${bundle}"): removing absolute rpath ${rp}"
        install_name_tool -delete_rpath "${rp}" "${bin}"
    done < <(otool -l "${bin}" | awk '/cmd LC_RPATH/{getline; getline; print $2}')
    if ! otool -l "${bin}" | awk '/cmd LC_RPATH/{getline; getline; print $2}' | grep -qx '@executable_path/../Frameworks'; then
        install_name_tool -add_rpath @executable_path/../Frameworks "${bin}"
    fi
    local stray=""
    while read -r ref; do
        case "${ref}" in
            @executable_path/*|@loader_path/*) ;;
            @rpath/*) [[ -e "${bundle}/Contents/Frameworks/${ref#@rpath/}" ]] || stray+="${ref} (not in Contents/Frameworks)"$'\n' ;;
            *) stray+="${ref}"$'\n' ;;
        esac
    done < <(otool -L "${bin}" | awk '/Qt/{print $1}')
    [[ -z "${stray}" ]] || { printf '%s' "${stray}" >&2; die "$(basename "${bundle}"): Qt libraries above resolve outside the bundle"; }
    [[ -d "${bundle}/Contents/PlugIns/platforms" ]] || die "$(basename "${bundle}"): no platform plugin"
}
say "verifying"
check_bundle "${APP}"
check_bundle "${VIEWER}"
[[ -n "$(find "${APP}/Contents/PlugIns" -name 'libqjpeg.dylib' | head -1)" ]] ||
    die "omegamaps.app: no jpeg image-format plugin; the Cisco icons would not draw"
[[ -n "$(find "${VIEWER}/Contents/Frameworks" -path '*QtWebEngineProcess.app*' -name QtWebEngineProcess -type f | head -1)" ]] ||
    die "omegamaps-viewer.app: no QtWebEngineProcess; the map page would never load"
[[ -n "$(find "${VIEWER}/Contents/Frameworks" -name 'qtwebengine_resources.pak' | head -1)" ]] ||
    die "omegamaps-viewer.app: no WebEngine resources; the map page would be blank"
say "both bundles self-contained; WebEngine helper and resources present; jpeg plugin present"

# --- sign --------------------------------------------------------------------
for b in "${APP}" "${VIEWER}"; do
    if [[ -n "${SIGN_ID}" ]]; then
        say "signing $(basename "${b}") with ${SIGN_ID}"
        codesign --force --options runtime --timestamp --deep --sign "${SIGN_ID}" "${b}"
    else
        say "ad-hoc signing $(basename "${b}") (local use; Gatekeeper will ask on other Macs)"
        codesign --force --deep --sign - "${b}"
    fi
    codesign --verify --deep --strict "${b}"
done

# --- dmg ---------------------------------------------------------------------
if [[ "${MAKE_DMG}" -eq 1 ]]; then
    mkdir -p dist
    DMG="dist/omegamaps-${VERSION}-${ARCH}.dmg"
    STAGE="$(mktemp -d)"
    cp -R "${APP}" "${VIEWER}" "${STAGE}/"
    ln -s /Applications "${STAGE}/Applications"
    say "writing ${DMG}"
    rm -f "${DMG}"
    hdiutil create -volname "omegamaps ${VERSION}" -srcfolder "${STAGE}" -ov -format UDZO "${DMG}" >/dev/null
    rm -rf "${STAGE}"
    ls -lh "${DMG}"
fi

echo
echo "open it with:"
echo "      open ${APP}"
