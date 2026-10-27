#!/usr/bin/env bash
# scripts/bundle-appimage.sh
#
# Packages omegamaps and its map viewer as one AppImage: a single executable
# file, no install, runs on any reasonably current distribution.
#
#   ./scripts/bundle-appimage.sh
#   ./scripts/bundle-appimage.sh --qt ~/Qt/6.10.3/gcc_64 --keep-appdir
#
# THE OTHER LINUX PACKAGE, beside bundle-linux.sh's tarball. The tarball needs
# no tooling; this needs linuxdeploy from GitHub and gives back one file with a
# desktop entry and an icon. The release workflow ships this one, because it
# also carries the viewer's WebEngine payload -- the helper process, its .pak
# resources and locales -- which linuxdeploy's Qt plugin knows how to place.
#
# BOTH PROGRAMS IN ONE IMAGE. The application starts the viewer from beside
# itself (app/viewerlaunch.cpp: <appdir>/omegamaps-viewer), so both land in
# AppDir/usr/bin and share one set of Qt libraries. Shipped apart, somebody
# gets an application whose "View map" has nothing to open.
#
# THE DOWNLOAD IS THE CATCH: linuxdeploy and its Qt plugin are fetched from
# github.com at build time. Set LINUXDEPLOY_DIR to a directory holding both
# AppImages to use copies you already have.
#
# Output: dist/omegamaps-<version>-x86_64.AppImage

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

QT_PREFIX="${OMEGAMAPS_QT:-}"
BUILD_DIR="build-appimage"
KEEP_APPDIR=0
LD_DIR="${LINUXDEPLOY_DIR:-}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --qt) QT_PREFIX="$2"; shift 2 ;;
        --qt=*) QT_PREFIX="${1#*=}"; shift ;;
        --build-dir) BUILD_DIR="$2"; shift 2 ;;
        --keep-appdir) KEEP_APPDIR=1; shift ;;
        -h|--help) sed -n '2,26p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

die() { echo "error: $*" >&2; exit 1; }
say() { echo "==> $*"; }

[[ "$(uname -s)" == "Linux" ]] || die "AppImages are built on Linux"

# --- Qt ----------------------------------------------------------------------
# One qmake decides it all: the prefix CMake builds against and the Qt the
# deploy plugin copies from. Two Qts, one for each, is a bundle that works on
# the build machine only.
if [[ -n "${QT_PREFIX}" ]]; then
    QMAKE="${QT_PREFIX}/bin/qmake"
    [[ -x "${QMAKE}" ]] || QMAKE="${QT_PREFIX}/bin/qmake6"
fi
if [[ -z "${QMAKE:-}" ]]; then
    QMAKE="$(command -v qmake6 || command -v qmake || true)"
fi
[[ -n "${QMAKE}" && -x "${QMAKE}" ]] || die "no qmake; pass --qt <prefix> or put Qt's bin on PATH"
export QMAKE
QT_PREFIX="$("${QMAKE}" -query QT_INSTALL_PREFIX)"
say "Qt $("${QMAKE}" -query QT_VERSION) at ${QT_PREFIX}"

# --- build -------------------------------------------------------------------
say "building into ${BUILD_DIR}"
cmake -S . -B "${BUILD_DIR}" -DCMAKE_BUILD_TYPE=Release -DCMAKE_PREFIX_PATH="${QT_PREFIX}" >/dev/null
cmake --build "${BUILD_DIR}" --target omegamaps omegamaps-viewer -j"$(nproc)"

APP="${BUILD_DIR}/app/omegamaps"
VIEWER="${BUILD_DIR}/app/omegamaps-viewer"
[[ -x "${APP}" ]] || die "no ${APP} after the build"
[[ -x "${VIEWER}" ]] || die "no ${VIEWER}: this Qt has no WebEngine, and the AppImage would ship without its viewer"
VERSION="$(tr -d '[:space:]' < "${BUILD_DIR}/app-version.txt")"
VERSION="${VERSION#v}"

# --- linuxdeploy -------------------------------------------------------------
if [[ -z "${LD_DIR}" ]]; then
    LD_DIR="${ROOT}/.linuxdeploy"
    mkdir -p "${LD_DIR}"
fi
fetch() {
    local name="$1" url="$2"
    [[ -x "${LD_DIR}/${name}" ]] && return
    say "downloading ${name}"
    curl -sSfL -o "${LD_DIR}/${name}" "${url}" ||
        die "could not download ${name}. No network? Set LINUXDEPLOY_DIR to a directory holding it."
    chmod +x "${LD_DIR}/${name}"
}
fetch linuxdeploy-x86_64.AppImage \
    "https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-x86_64.AppImage"
fetch linuxdeploy-plugin-qt-x86_64.AppImage \
    "https://github.com/linuxdeploy/linuxdeploy-plugin-qt/releases/download/continuous/linuxdeploy-plugin-qt-x86_64.AppImage"
# linuxdeploy is itself an AppImage and needs FUSE, which containers and CI
# images often lack; extract-and-run sidesteps that.
export APPIMAGE_EXTRACT_AND_RUN=1
export PATH="${LD_DIR}:${PATH}"

# --- AppDir ------------------------------------------------------------------
APPDIR="${ROOT}/AppDir"
say "assembling ${APPDIR}"
rm -rf "${APPDIR}"
mkdir -p "${APPDIR}/usr/bin"
cp "${APP}" "${VIEWER}" "${APPDIR}/usr/bin/"

# --- pack --------------------------------------------------------------------
# Both executables named, so linuxdeploy resolves the libraries of each and the
# Qt plugin sees WebEngine in the viewer's and deploys QtWebEngineProcess with
# its resources. The jpeg image-format plugin is named explicitly: the Cisco
# icons need it and nothing links it, so a plugin chooser could leave it out.
say "running linuxdeploy"
mkdir -p dist
export EXTRA_PLATFORM_PLUGINS="libqxcb.so"
export EXTRA_QT_PLUGINS="imageformats"
OUTPUT="dist/omegamaps-${VERSION}-x86_64.AppImage" \
linuxdeploy-x86_64.AppImage \
    --appdir "${APPDIR}" \
    --executable "${APPDIR}/usr/bin/omegamaps" \
    --executable "${APPDIR}/usr/bin/omegamaps-viewer" \
    --desktop-file packaging/linux/omegamaps.desktop \
    --icon-file packaging/linux/omegamaps.png \
    --plugin qt \
    --output appimage
IMAGE="dist/omegamaps-${VERSION}-x86_64.AppImage"
[[ -f "${IMAGE}" ]] || die "linuxdeploy produced no ${IMAGE}"

# --- verify ------------------------------------------------------------------
# What a clean machine needs and the build machine would paper over. Each one
# missing is a failure that shows nothing until somebody runs it elsewhere: no
# viewer, a blank map page, icons that never draw.
say "verifying"
CHECK="$(mktemp -d)"
( cd "${CHECK}" && "${ROOT}/${IMAGE}" --appimage-extract >/dev/null 2>&1 ) || die "the AppImage will not extract"
R="${CHECK}/squashfs-root"
missing=()
[[ -x "${R}/usr/bin/omegamaps" ]] || missing+=("usr/bin/omegamaps")
[[ -x "${R}/usr/bin/omegamaps-viewer" ]] || missing+=("usr/bin/omegamaps-viewer")
[[ -n "$(find "${R}" -name QtWebEngineProcess -type f | head -1)" ]] || missing+=("QtWebEngineProcess")
[[ -n "$(find "${R}" -name 'qtwebengine_resources.pak' | head -1)" ]] || missing+=("qtwebengine_resources.pak")
# Only when the Qt has one: a distribution's WebEngine built against the
# system ICU (Ubuntu's) ships no icudtl.dat and needs none.
if [[ -f "$("${QMAKE}" -query QT_INSTALL_DATA)/resources/icudtl.dat" ]]; then
    [[ -n "$(find "${R}" -name 'icudtl.dat' | head -1)" ]] || missing+=("icudtl.dat")
fi
[[ -n "$(find "${R}" -path '*qtwebengine_locales*' -name '*.pak' | head -1)" ]] || missing+=("qtwebengine_locales/*.pak")
[[ -n "$(find "${R}" -name 'libqxcb.so' | head -1)" ]] || missing+=("platforms/libqxcb.so")
[[ -n "$(find "${R}" -name 'libqjpeg.so' | head -1)" ]] || missing+=("imageformats/libqjpeg.so")
[[ -n "$(find "${R}" -name 'libqsvg.so' | head -1)" ]] || missing+=("imageformats/libqsvg.so")
rm -rf "${CHECK}"
[[ ${#missing[@]} -eq 0 ]] || die "missing from the AppImage: ${missing[*]}"
say "both programs, the WebEngine payload, xcb and the jpeg/svg plugins are inside"

[[ "${KEEP_APPDIR}" -eq 1 ]] || rm -rf "${APPDIR}"
ls -lh "${IMAGE}"
echo
echo "run it with:"
echo "      ${IMAGE}"
