# Building omegamaps — quick reference

The commands you actually type, for every platform and for a release. The
reasons, the dependency table and the troubleshooting history are in
[docs/BUILDING.md](docs/BUILDING.md).

You need Go 1.25, CMake 3.19+, and Qt 6 with Svg — plus Qt WebEngine (with
WebChannel and Positioning) for the map viewer. Without WebEngine everything
builds except the viewer.

## Day to day

### Linux

```bash
./scripts/build-app.sh
./build/app/omegamaps
```

`build-app.sh` finds Qt (pass `--qt ~/Qt/6.10.3/gcc_64` to choose one),
configures, builds both programs into `build/app/`, and says which Qt the
result runs against. `--clean` starts the CMake cache over; `--list` shows
every Qt it can find and builds nothing.

### macOS

```bash
./scripts/build-app.sh
open build/app/omegamaps.app
```

Same script. On a Mac it also picks an SDK the linker can actually read
(`scripts/mac-sdk.sh`) — a Command Line Tools update can install one it
cannot, and every C/C++ build then fails at CMake's first compiler check with
`tapi error: malformed file`. To build by hand instead:

```bash
export SDKROOT="$(scripts/mac-sdk.sh -v)"
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_PREFIX_PATH="$(brew --prefix qt)" -DCMAKE_OSX_SYSROOT="$SDKROOT"
cmake --build build -j"$(sysctl -n hw.ncpu)"
```

Keep the `export` in the same shell as the build: the Go archive step reads
`SDKROOT`, not CMake's setting.

### Windows

From an *x64 Native Tools Command Prompt for VS 2022*, with mingw-w64 `gcc`
also on `PATH` (cgo needs it):

```bat
scripts\bundle-windows.bat --qt C:\Qt\6.10.3\msvc2022_64
dist\omegamaps\omegamaps.exe
```

### Command-line tools only (any OS, Go only)

```bash
./scripts/build.sh
```

Vets, tests, and builds `crawl`, `mapview` and `omvault` into `build/bin/`.
`--cross` builds every release platform as well.

## Checking a change

```bash
go test ./...
QT_QPA_PLATFORM=offscreen ./build/tests/icons_probe
QT_QPA_PLATFORM=offscreen ./build/tests/viewer_probe "" /tmp/omv
```

`icons_probe` also confirms the Qt has the jpeg image-format plugin the Cisco
icons need. `crawl_probe` and `replay_probe` are in docs/BUILDING.md.

## Screenshots

```bash
./scripts/demo-screenshots.sh
```

Crawls the synthetic demo network (`cmd/demonet`, ~150 nodes on
`example.net`), replays the crawl through the window and opens the map in the
viewer: 14 grabs in light and dark, a full-map PNG and a draw.io export, in
`demo-shots/`. Nothing from a real network, and nothing of yours, is touched.
Build first; on a Mac it draws real windows for about a minute — leave them
alone.

## Packages

| Platform | Command | Result |
|---|---|---|
| Linux | `./scripts/bundle-appimage.sh` | `dist/omegamaps-<ver>-x86_64.AppImage` |
| macOS | `./scripts/bundle-mac.sh` | `dist/omegamaps-<ver>-arm64.dmg` |
| Windows | `scripts\bundle-windows.bat --zip` | `dist\omegamaps-windows-x64.zip` |

Each carries both programs, the viewer's WebEngine payload and the image
plugins, and checks for them before it finishes. The AppImage downloads
linuxdeploy from GitHub the first time. The dmg is ad-hoc signed unless you
pass `--sign "Developer ID Application: ..."`, so on another Mac the first
launch is right-click → Open.

## Releases (GitHub Actions)

`.github/workflows/release.yml` builds all three packages on GitHub's runners
with the scripts above, and checks each one by opening the demo map in the
packaged viewer.

Try a build without releasing — artifacts appear on the run's page:

```bash
gh workflow run release.yml --ref main
gh run watch
```

Release: bump the version, tag, push. The tag must match the version or the
run stops at the first job.

```bash
sed -i 's/project(omegamaps VERSION [0-9.]*/project(omegamaps VERSION 0.1.3/' CMakeLists.txt
git commit -am "v0.1.3" && git push
git tag v0.1.3 && git push origin v0.1.3
```

(On a Mac, `sed -i ''` instead of `sed -i`.) The tag push builds the three
packages and publishes them, with `SHA256SUMS.txt`, as a GitHub release.

If a job fails, this pulls just the failing step's log:

```bash
gh run view --log-failed > run.log
```
