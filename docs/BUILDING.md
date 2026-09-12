# Building omegamaps

## What you need

| | Version | Notes |
|---|---|---|
| Go | 1.25 to build the shipping dependency set; 1.22 is the floor | `./test.sh -F` checks the floor |
| C compiler | optional for the tools | `go test -race` and the Qt application need one; the tools are pure Go |
| CMake | 3.19 or newer | the Qt application only |
| Qt | 6.2 or newer: Widgets, Svg | the Qt application only; Svg draws the device icons |
| Qt WebEngine | same Qt, with Quick, WebChannel, Positioning | optional: the map viewer only. Without it everything else builds |
| C++ toolchain | C++17 | the Qt application only; MSVC on Windows, with mingw-w64 gcc on `PATH` for cgo |

The command-line tools need only Go. Everything below the first table row is
for the Qt application.

### Where it has been built

| | Tools | Qt application |
|---|---|---|
| Linux | built, crawled the IOSv lab over SNMP | built with installer Qt 6.10 (`build-app.sh`), replays a recording, and runs the map viewer; with distro Qt 6.4, crawls the fake lab from the form and passes all three probes |
| macOS | cross-built (`build.sh --cross`) | built with Homebrew Qt and run: replaying a large recording through all three columns; the map viewer, opened from the application |
| Windows | built and run | built with MSVC 2022 Build Tools and Qt 6.10.3 `msvc2022_64`, bundled by `scripts\bundle-windows.bat`: crawls a live lab over SSH and over SNMP from the form, and opens the map in the viewer |

## Quick start

```bash
./scripts/build.sh                 # Linux, macOS: vet, test, build
scripts\build.bat                  # Windows
```

The Qt application is built separately: `./scripts/build-app.sh`, or
`scripts\bundle-windows.bat` on Windows.

Binaries land in `build/bin/`: `crawl`, `mapview`, `omvault`. `build/` is
gitignored; the CMake build below shares it.

```bash
./scripts/build.sh --no-test       # build only
./scripts/build.sh --cross         # also darwin/arm64, darwin/amd64, linux/amd64,
                                   # linux/arm64, windows/amd64 -> build/cross/
./scripts/build.sh --version v0.2.0
```

Every tool reports what it is with `-version`:

```
crawl v0.2.0 (go1.25.0 darwin/arm64)
```

The version is `git describe` unless `--version` says otherwise. A binary that
was not stamped -- `go run`, a plain `go build`, a build outside a git
checkout -- reports the revision the toolchain recorded, or `dev`. It never
reports a number it was not given.

## By hand

```bash
CGO_ENABLED=0 go build -trimpath -o build/bin/ ./cmd/...
```

## Cross-compiling

Nothing in the tools uses cgo, so any machine builds every platform:
`--cross`, or `GOOS`/`GOARCH` by hand with `CGO_ENABLED=0`. The build scripts
set `CGO_ENABLED=0` on purpose -- a dependency that starts to need cgo fails
the build rather than producing a binary that wants a C runtime on the target.

This stops being true for the Qt application: its Go side is a c-archive, which
needs cgo and the target's own toolchain, so it is built on each platform the
way omegassh is.

`capi/` is that c-archive, and every file in it is `//go:build cgo`. With cgo
off -- the build scripts, `--cross`, a machine with no C compiler -- `./...`
skips it rather than failing on it.

## The Qt application

```bash
./scripts/build-app.sh                         # finds Qt, configures, builds
./scripts/build-app.sh --qt ~/Qt/6.8.3/gcc_64  # or says which Qt to use
./build/app/omegamaps run.jsonl                # replay a recording, 10x
./build/app/omegamaps run.jsonl --speed 1      # real time; 0 is instant
```

`build-app.sh --list` shows every Qt it can see and why each one would or
would not do. It needs 6.2 or newer with Widgets and Svg, and tries `--qt`,
`$OMEGAMAPS_QT`, `$CMAKE_PREFIX_PATH`, `~/Qt/<version>/gcc_64` (and `/opt/Qt`)
newest first, `qmake6`, then the distribution's Qt.

It also guards the CMake cache, which is where "CMake cannot find Qt" usually
comes from. The cache records the Qt found on the first configure -- or that
none was -- once per Qt module, and a later configure with a corrected
`CMAKE_PREFIX_PATH` does not look again; a directory pointed at a second Qt
links a mix of both. The script drops the cache whenever any cached
`Qt6*_DIR` is outside the Qt it chose, and after building says which QtCore
the application actually loads. `--clean` drops the cache unconditionally;
`--probe run.jsonl map.json` runs replay_probe after the build.

By hand, the same thing is:

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_PREFIX_PATH=<qt prefix>
cmake --build build -j
```

and if that ever finds the wrong Qt, `rm build/CMakeCache.txt` before trying
again -- changing the prefix alone will not.

`build/app/omegamaps` is the application; `build/tests/replay_probe` and
`build/tests/crawl_probe` check it (below). A cold build is about half a minute, most of it the Go archive.

The Go archive is a custom command inside the CMake build, so **the Go
environment comes from the shell that runs `cmake --build`**, not from the
configure step. Whatever `GOFLAGS`, `GOPROXY` or `GOTOOLCHAIN` a plain
`go build` needs on your machine, `cmake --build` needs in the same shell.

A failed Go archive stops the build before anything C++ is linked, and every
binary in `build/` keeps its previous contents: a stale application that still
runs. If a change seems not to have taken, check the binary's timestamp before
the source, and read the build output for `error` rather than the summary.

Re-run the configure step after adding a source file or a target; CMake does
not notice a new one on its own and says `No rule to make target`.

What the application is today: Secure Cartography's layout over omegamaps'
crawler. The crawl form (connection, credentials from the vault, options,
output, Start / Test single / Stop) builds the request the C surface takes
(`include/omegamaps/omegamaps.h`), defaults come from the library so the window
and `crawl` agree, and the fields are remembered between runs. The vault is
its own surface (`include/omegamaps/vault.h`); the credentials panel unlocks,
creates, adds and removes, and the application tries the OS keyring and
`OMEGAMAPS_VAULT_PASSWORD` at startup without prompting. Every crawl writes its
map, its recording and its log side by side; afterwards the speed selector
replays it. The run panel's status line says what is happening -- running,
at which depth, for how long -- and how the last run ended: completed, stopped,
or failed, with the counts.

### Qt from aqtinstall

The Qt trees for these projects come from [aqtinstall](https://aqtinstall.readthedocs.io),
which is a pip tool and so is usually not on `PATH` in a fresh shell. On these
machines it lives in a venv, `~/venvs/qt`; either activate that first or call
it by path:

```bash
source ~/venvs/qt/bin/activate          # then plain `aqt ...`
~/venvs/qt/bin/aqt version              # or by path, no activation
```

Setting it up on a new machine (Ubuntu 23.04 and later refuse a system-wide
`pip install`; the venv sidesteps that):

```bash
sudo apt install python3-venv           # if `python3 -m venv` complains about ensurepip
python3 -m venv ~/venvs/qt
~/venvs/qt/bin/pip install aqtinstall
```

`build-app.sh` looks for aqt on `PATH`, then `$OMEGAMAPS_AQT`, then
`~/venvs/qt/bin/aqt`, and prints any aqt command it suggests with the path that
will run.

Not every tree under `~/Qt` is aqt's. When the Qt installer is there too, the
versions it put down are its own (listed in `~/Qt/components.xml`); adding
modules to one of those with aqt works, but the Maintenance Tool will not know
about them.

#### aqt on Windows

A `pip install --user` puts `aqt.exe` under
`%APPDATA%\Python\Python3xx\Scripts`, which is not on `PATH`; a venv puts it in
that venv's `Scripts`, not `bin`. To find whichever a machine has:

```bat
where /r %USERPROFILE% aqt.exe
```

Set `OMEGAMAPS_AQT` to the full path and `build-app.sh` prints any aqt command
it suggests with a path that will run.

WebEngine exists for MSVC kits only -- there is no mingw build of it:

```bat
set AQT=%APPDATA%\Python\Python312\Scripts\aqt.exe
%AQT% list-qt windows desktop --modules 6.10.3 win64_msvc2022_64
%AQT% install-qt windows desktop 6.10.3 win64_msvc2022_64 --noarchives ^
    -m qtwebengine qtwebchannel qtpositioning -O C:\Qt
```

`--noarchives` only when the kit already has a complete Qt Quick. See
**Getting WebEngine**, under The map viewer, for how that goes wrong and what
it looks like when it does.

### Windows

Built and run. mingw-w64 compiles the Go archive and MSVC links the
application. The three seams that makes necessary are carried over from
omegassh unchanged: the `.CRT$XCU` runtime shim (`msvc/runtime_init.c`), the
`.pdata` sort (`scripts/pdatafix`), and `legacy_stdio_definitions`. omegassh's
`docs/WINDOWS.md` explains each.

What it needs: MSVC 2022 Build Tools (which bring their own CMake), mingw-w64
gcc on `PATH` for cgo, and a Qt `msvc2022_64` kit -- with WebEngine if the map
viewer is wanted.

```bat
scripts\build.bat                  REM vet, test, the command-line tools
scripts\bundle-windows.bat         REM the Qt application, staged into dist\
```

`bundle-windows.bat` guesses a Qt under `C:\Qt` and takes `--qt` or
`OMEGAMAPS_QT` to override, configures into `build-win\`, then stages
`dist\omegamaps\` with windeployqt: the platform plugin, the Qt DLLs, Svg,
WebEngine's resources and its locale `.pak` files, and the MSVC runtime copied
out of the Build Tools' redistributable directory. It checks that one Qt is
used throughout rather than a mix, and says so.

Run it with `dist\omegamaps\omegamaps.exe`.

Without WebEngine in the chosen Qt the bundle still succeeds and says so: the
application works, and opening a map reports that the viewer is not installed.
That is the intended degradation -- the viewer is a separate executable, so
there is still an application without it.

Tests run without `-race` on Windows, which needs cgo and mingw; the race build
is covered by `scripts/build.sh` on Linux and macOS.

### macOS

Built and run. `CoreFoundation` and `Security` are on the link line because
Go's `crypto/x509`, reached through `x/crypto/ssh`, imports from
Security.framework; the first macOS build is what confirms the link line is
complete. `build-app.sh` finds an installer or aqt Qt under
`~/Qt/<version>/macos`, or Homebrew's through `qmake6`/`qtpaths6` on `PATH`,
and checks what the binary loads with `otool` rather than `ldd`. With
Homebrew's Qt the native file panel does not appear; the file dialogs fall
back to Qt's own (see the map viewer, below).

Not yet exercised on macOS: a live crawl from the form, the keyring unlock
(Keychain), and a bundle. One display fault shows there first, because macOS
maps a point to a pixel where Linux uses 96 dpi: text sized in fixed points
(captions, stat labels, the log) comes out about a quarter smaller.

Fixed after the first Mac runs: in a window shorter than the run column needs,
the progress panel was squeezed below its natural height and re-expanded at
every depth boundary. The panel now keeps its height, its waiting list keeps
its lines for the whole run, and the column scrolls when the window is too
short; replay_probe runs in the smallest window and checks all three.

### The map viewer

`omegamaps-viewer` is internal/mapweb's page -- the `mapview` harness's
viewer, with its PNG, JSON and draw.io exports -- in a QWebEngineView, as its
own executable. The application starts it: **View map** in the run panel for
the run's map, **Open map...** in the header for any map. It is written beside
the application (`build/app/omegamaps-viewer`; on macOS a sibling bundle,
`build/app/omegamaps-viewer.app`), and runs on its own too:

```bash
./build/app/omegamaps-viewer --theme dark lab-map.json
```

The viewer runs the loopback server in-process (`include/omegamaps/mapview.h`)
and shows the page in the application's theme. Exports -- File > Export, or
the page's own buttons -- go through a save dialog. Saved layouts are files
under `~/.omegamaps/layouts`, one per map by path, not the browser's storage,
which is per port and so never outlived a run; `mapview -layouts` uses the
same directory.

**Finding the viewer.** Where it is, is a setting (`viewer/path`). The
application looks at `$OMEGAMAPS_VIEWER`, then the setting, then beside
itself. When none has it -- an application run from an IDE's build directory,
a viewer built elsewhere -- the dialog offers **Locate...**; the pick (on macOS
the `.app` or the program inside it) is checked and remembered. The check runs
it with `--version` and wants `omegamaps-viewer` back, so the application
chosen by mistake is refused; on Windows, where a windowed program answers
`--version` with a message box, it checks only that the file is a program. On
macOS a bundled viewer is started through `open -n -a`, which brings it to the
front and reports a failed launch. Every launch is noted in the discovery log.

**Opening files.** Open map..., Open recording..., Locate... and the viewer's
File > Open ask for a path first: typed (with completion), pasted (`~`
expanded, quotes and `file://` removed) or dragged from the file manager,
with Browse... beside it. Every file dialog in both programs tries the
platform's first. One that returns empty in under a quarter of a second never
appeared, and the process switches to Qt's own file dialogs for the rest of
the session and asks again, saying so on stderr. That is what happens with
Homebrew Qt on macOS, where the native panel returns at once without showing,
for every dialog -- the cause is not known. `OMEGAMAPS_QT_DIALOGS=1` starts in
Qt's dialogs.

**Getting WebEngine.** It has to come from the same Qt the build uses, and it
is not self-contained: WebEngineWidgets needs Qt Quick and QuickWidgets, and
WebEngineCore needs Quick, WebChannel and Positioning. Qt Quick is
qtdeclarative, one of base Qt's own archives rather than a module, so an
aqtinstall tree made the omegassh way (`--archives qtbase`) has none of it.
By where the Qt came from:

- **aqtinstall.** Add what is missing in place. With Qt Quick already there
  (`lib/cmake/Qt6Quick` exists), the modules alone: `--noarchives -m
  qtwebengine qtwebchannel qtpositioning`. Without it, Quick's archive too:
  `--archives qtdeclarative` installs that one archive of base and no other.
  Check the names for the release first:

  ```bash
  aqt list-qt linux desktop --archives 6.10.2 linux_gcc_64     # qtdeclarative among them
  aqt list-qt linux desktop --modules 6.10.2 linux_gcc_64      # qtwebengine, qtwebchannel, qtpositioning
  aqt install-qt linux desktop 6.10.2 linux_gcc_64 --archives qtdeclarative \
      -m qtwebengine qtwebchannel qtpositioning -O ~/Qt
  ```

  A fresh aqt install for the whole application, in omegassh's form:

  ```
  Linux     6.10.2 linux_gcc_64       --archives qtbase qtsvg qtdeclarative -m qtwebengine qtwebchannel qtpositioning
  macOS     6.10.2 clang_64           the same
  Windows   6.10.2 win64_msvc2022_64  the same; WebEngine exists for MSVC kits only
  ```

- **The Qt installer.** A version it put down is listed in its
  `components.xml`; use its Maintenance Tool, so it keeps knowing what is
  installed: Qt WebEngine, Qt WebChannel and Qt Positioning under Additional
  Libraries (Quick is in the base install). It will not add components until
  its own pending update is done.
- **Homebrew.** `brew install qtwebengine`; it brings its own dependencies.
- **Ubuntu's Qt.** `qt6-webengine-dev` brings the lot.

A package for one of these does nothing for a Qt from another.
`build-app.sh` works out which kind of Qt it chose and prints the command for
it; the CMake warning gives Qt's own reason and names the modules the Qt
lacks; `build-app.sh --list` marks a Qt with WebEngine but without its
dependencies. `-DOMEGAMAPS_BUILD_VIEWER=OFF` skips the viewer.

**When the configure fails on a QML plugin DLL.** A kit can carry Qt Quick's
CMake metadata while the binaries those files describe are absent -- metadata
from one install, binaries from another that never happened. Every config file
`build-app.sh` looks for then exists, and `find_package(Qt6 ...
WebEngineWidgets)` still fails, several frames away from anything the project
asked for:

```
CMake Error at .../Qt6Qml/QmlPlugins/Qt6LabsPlatformpluginTargets.cmake:115:
  The imported target "Qt6::LabsPlatformplugin" references the file
     ".../qml/Qt/labs/platform/labsplatformplugind.dll"
  but this file does not exist.
```

Read the call stack rather than the message: `WebEngineWidgets ->
WebEngineCore -> Quick -> Qml -> LabsPlatform`. Nothing is wrong with such a Qt
until WebEngine pulls Quick in, which is why it surfaces the first time the
viewer is built against it and never before.

The tell is a plugin directory holding `Targets-debug.cmake` and
`Targets-relwithdebinfo.cmake` with **no** `Targets-release.cmake`, and no
matching directory under `qml/`:

```bat
dir C:\Qt\6.10.3\msvc2022_64\qml\Qt\labs\platform\
dir C:\Qt\6.10.3\msvc2022_64\lib\cmake\Qt6Qml\QmlPlugins\Qt6LabsPlatformplugin*
```

Qt includes those config fragments by glob and checks every configuration it
finds for file existence at `find_package` time, so `CMAKE_BUILD_TYPE=Release`
does not avoid it. The repair is to install the archive the binaries come from,
over the top:

```bat
%AQT% install-qt windows desktop 6.10.3 win64_msvc2022_64 ^
    --archives qtdeclarative -O C:\Qt
```

Installing `qtdeclarative` over a kit that already has it is harmless and
repairs a partial extraction, so when in doubt prefer `--archives
qtdeclarative` to `--noarchives`.

`build-app.sh` does not catch this, deliberately. Testing for a `qml/` runtime
was tried and reverted: WebEngineWidgets needs Qt Quick's *libraries*, not its
QML module tree, so a Qt with no `qml/` directory at all builds the viewer and
passes every probe -- Ubuntu's Qt 6.4 is one. The test rejected a working Qt,
which is worse than the failure it was meant to prevent.

**Where it has run.** Linux: built and probed with Ubuntu's Qt 6.4, and built
and used with an installer Qt 6.10.2. macOS: built with Homebrew Qt and used
from the application -- a map of about 850 nodes, draw.io export opened in
draw.io. Windows: built with Qt 6.10.3 `msvc2022_64`, staged by
`bundle-windows.bat`, and the map opened in the viewer from the bundle.

There is no deploy step for macOS yet: macdeployqt must carry WebEngine's
helper process and its resources into the bundle, and that has not been tried.
`QtWebEngineProcess`, `icudtl.dat`, `qtwebengine_resources*.pak` and the locale
`.pak` directory all have to ship, and missing any of them gives a blank page
rather than an error -- worth a positive check in the bundling step rather than
a discovery afterwards.

### replay_probe

```bash
QT_QPA_PLATFORM=offscreen ./build/tests/replay_probe run.jsonl map.json /tmp/om 20
```

Plays the recording through the real window with no display and checks what
the views show against the run model: every dialed device on the map and
nothing else, final counts, one log result per finished device, every
not-dialed device in the log, and links drawn from map.json. It saves
`/tmp/om-light.png`, `-dark.png`, `-cyber.png`, `-about.png` (the About box,
opened from the header's wordmark), and at a speed above zero a
`-running.png` from partway through. It runs in the smallest window the
application allows, and at a speed above zero samples the progress panel
through the run: never squeezed below its natural height, never shrinking
mid-run. `-` in place of map.json skips the map
checks. Exit status is the number of failed checks.

### crawl_probe and the fake lab

```bash
sudo ./build/tests/fakelab -addrs | sudo sh    # once per boot: the lab's addresses
sudo QT_QPA_PLATFORM=offscreen ./build/tests/crawl_probe /tmp/om-crawl
```

A real crawl, through the C surface and the window, against a fake copy of
the home lab: six IOS devices (`internal/fakedev/lab.go`) on the lab's own
addresses, port 22 -- wan-core-1, usa-rtr-1, eng-rtr-1, the eng spine pair
and eng-leaf-1. The names do not resolve, so every device past the seed is
reached the way the real lab's are: by name, failing, then at the address its
neighbor reported. eng-leaf-1 rejects the lab credential, and eng-spine-2
reports a Linux host that the probe's exclude pattern leaves undialed, so all
three outcomes appear.

It checks the vault surface (secrets never come back out, meta-only edits keep
them, wrong and quiet unlocks answer correctly), request validation, the
crawl's counts and rows, the map, recording and log written beside each other,
host keys going to the file the request named, the recording replaying to the
same run, and a cancel mid-crawl -- then does the crawl again through the
form: the credentials panel over the vault, a bad seed marked and the start
refused, a locked vault refused, Test single, Start, the buttons following the
run, View map starting the viewer (a stand-in, via `$OMEGAMAPS_VIEWER`) on the
run's map in the current theme, and the fields remembered by a second window. It saves
`/tmp/om-crawl-light.png` and `/tmp/om-crawl-form.png`, and keeps its settings
out of yours.

The lab needs root, once for the addresses and again to bind port 22, so on
a workstation this is a `sudo` run or none at all. Without the addresses the
probe exits 77 and says what to run -- a skip, not a failure.
`go test ./internal/crawldial -run TestCrawlTheFakeLab` is the same crawl
without the window, and skips the same way.

### viewer_probe

```bash
QT_QPA_PLATFORM=offscreen ./build/tests/viewer_probe [map.json] [/tmp/omv]
```

Opens the viewer window on the fake lab's map (or the one given) with no
display and checks it end to end: every node the server loaded drawn, the
token out of the address, the theme on the page and changed live, the three
File > Export writes (a PNG that decodes, JSON with the map's devices, draw.io
with one vertex per visible node), a real click on the page's JSON button
downloading through the save dialog and replacing the chosen file, a cancel
writing nothing, navigation off the server refused, a non-map refused with the
map left loaded, Reload re-reading the file, and a layout saved in one window
restored in a second window on another port. It saves `/tmp/omv-light.png`,
`-dark.png` and `-cyber.png`, and keeps its settings and layouts out of yours.
As root, Chromium also needs `QTWEBENGINE_DISABLE_SANDBOX=1`.

## Testing

```bash
./test.sh               # build, gofmt, vet, tests
./test.sh -F            # also against the floor set in scripts/floor.deps
```

`scripts/build.sh` runs vet and the tests itself, with `-race` when a C compiler
is available.

### Against a real agent

The SNMP tests that need a live agent run when one is named, and are skipped
-- loudly, by `scripts/build.sh` -- when it is not:

```bash
PFSNMP_TEST_TARGET=10.0.0.11 PFSNMP_TEST_COMMUNITY=... ./scripts/build.sh
PFSNMP_TEST_TARGET=10.0.0.11 PFSNMP_TEST_COMMUNITY=... \
    go test ./internal/snmpprobe ./internal/crawldial -run Live -v
```

`PFSNMP_TEST_PORT` overrides 161. For v3: `PFSNMP_TEST_V3_USER`,
`PFSNMP_TEST_V3_AUTH`, `PFSNMP_TEST_V3_AUTHPROTO`, `PFSNMP_TEST_V3_PRIV`,
`PFSNMP_TEST_V3_PRIVPROTO`. Pointed at a switch, `TestProbeLive -v` prints the
neighbor records a crawl would receive -- the quickest check of a platform
before it is in a crawl.

A local net-snmp agent covers the transport, the credential ladder and the
bindings, but has no LLDP-MIB or CDP-MIB: neighbor parsing is covered by the
fixture tests until real captures replace them.

## Event stream

`crawl -events run.jsonl` records every event of a crawl as JSON Lines: a
header line (`{"schema":"omegamaps.events","version":1}`), then one event per
line in sequence order -- depth batches, admissions, each device's phases
(`snmp probe`, `ssh dial`, `ssh: <command>`, ...), credential outcomes,
fallbacks, and results. It is a side file; map.json is unchanged by it.

It is the same stream a front end shows. `crawlrun.ReadEvents` and
`crawlrun.Replay` rebuild the run's rows, per-depth progress and decisions from
a recording, so views can be built and tested against a real crawl without
running one; `crawlrun.Play` feeds it back at the recorded pace, scaled, which
is what the Qt application's replay uses. Field names are stable snake_case; fields may be added, and
readers ignore what they do not know.

## Configuration

Everything lives in `~/.omegamaps/`: `vault.json`, the SSH and SNMP binding
stores beside it, and saved profiles. The master password comes from the
prompt, `OMEGAMAPS_VAULT_PASSWORD`, or the OS keyring (`omvault keyring set`;
`OMEGAMAPS_NO_KEYRING=1` bypasses it for a run).

The vault format is shared with Pathfinder and Omega, so `-vault` can open
either one's file on purpose. Nothing opens one by default.