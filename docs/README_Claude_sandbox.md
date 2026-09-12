# README_Claude_sandbox.md

Recipe for building and testing omegamaps in a Claude sandbox, so changes get
**built and tested** there rather than reasoned about from reading the source.

The omegassh equivalent is `README_Claude_Qt_sandbox.md` in omegasshqt; this
project uses its approach, Qt half included (section 3a). The one hard part is
the same: the Go module cannot reach its proxy. Here the answer is the floor
dependency set, applied either to a throwaway copy (`./test.sh -F`) or to the
working tree with a byte-exact revert (`scripts/sandbox-floor.sh`).

Verified end to end on Ubuntu 24.04 with Go 1.22.2, net-snmp 5.9.4, CMake
3.28.3 and Qt 6.4.2.

---

## 0. What the sandbox is

A Linux container (Ubuntu 24.04, root) with a shell and a file system, where
Claude builds the code, runs it, and runs its tests before handing anything
over. Its properties decide what can be proved there:

- **Network egress is an allowlist.** GitHub, PyPI, npm and the Ubuntu archives
  are reachable. The Go module proxy, `sum.golang.org`, `golang.org` and
  `gopkg.in` are not -- which is the one hard problem, handled in section 2.
- **Files arrive and leave as uploads and downloads.** The project comes in as
  an uploaded zip or a clone from GitHub and goes back out as a zip. The
  sandbox can be reset between sessions; nothing in it is the source of truth.
- **The tool shell is `/bin/sh` (dash), and background processes do not
  survive between commands.** Anything a test needs running -- an SNMP agent --
  is started in the same command as the test (sections 4 and 7).
- **There is no terminal, no OS keyring, no network gear, and no macOS or
  Windows.** Terminals can be simulated (section 6); the rest is listed in
  section 10 as what the sandbox cannot tell you.

What it is good at: every package built and tested on the floor dependency set
under the race detector, SNMP exercised against a real agent, the real binaries
run end to end, and intermittent failures reproduced by volume.

---

## 1. Toolchain

```bash
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y golang-go snmpd snmp \
    cmake qt6-base-dev qt6-svg-dev qt6-webengine-dev
```

`golang-go` is 1.22.2, which is the project's floor, not its shipping
toolchain (1.25). Everything below runs on the floor set. `snmpd`/`snmp` are
for the live SNMP tests. `cmake`, `qt6-base-dev` and `qt6-svg-dev` are the Qt
application: Widgets plus the offscreen platform plugin, and QtSvg for the
device icons. `qt6-webengine-dev` is the map viewer's; without it the rest
builds and CMake warns. Ubuntu's Qt is 6.4.2, inside the 6.2 floor. g++ is already in
the image. A 403 from a third-party apt repo (nodesource) is
noise; the Ubuntu archives are on the allowlist.

## 2. The Go module cannot use its proxy

`proxy.golang.org`, `sum.golang.org`, `golang.org` and `gopkg.in` are not on the
egress allowlist; `github.com` is. The `golang.org/x/*` modules are fetched from
their GitHub mirrors through `scripts/floor.local`, which is gitignored and so
never arrives with the repo. Create it:

```
// Sandbox only (gitignored): golang.org/x resolved from GitHub mirrors.
replace (
	golang.org/x/crypto => github.com/golang/crypto v0.31.0
	golang.org/x/sys => github.com/golang/sys v0.28.0
	golang.org/x/term => github.com/golang/term v0.27.0
	golang.org/x/net => github.com/golang/net v0.21.0
	golang.org/x/text => github.com/golang/text v0.21.0
)
```

and use this environment for every Go command:

```bash
export GOTOOLCHAIN=local GOPROXY=direct GOSUMDB=off GOPRIVATE='*' GOFLAGS=-mod=mod
```

Two ways to use it:

- **Tests: `./test.sh -F`.** It copies the tree, applies `floor.deps` and
  `floor.local` to the copy, and runs everything there. The working tree is
  never touched. The main stage reports "skipped" because the toolchain is
  older than go.mod's 1.25 -- expected; the floor stage is the result.
- **Binaries, or iterating on one package: `scripts/sandbox-floor.sh apply`.**
  It backs up `go.mod`/`go.sum` and applies the same two files in place.
  `scripts/build.sh` then works as documented, and warns on every run that the
  floor is applied. `scripts/sandbox-floor.sh revert` restores the backup byte
  for byte; `status` says which state the tree is in.

**Go commands against a clean tree fail**, with `go.mod requires go >= 1.25.0
(running go 1.22.2)`. That is the shipping go.mod doing its job; apply the floor
first. Forgetting after a revert is the easy way to hit it.

**Revert before delivering anything.** A go.mod with mirror replaces in it is a
bad delivery, and `status` exits non-zero on one even if the backup is gone.

`go mod tidy` cannot finish here -- it walks the tests of dependencies, and
gosnmp's reach `gopkg.in/yaml.v3` through testify. Nothing needs it:
`GOFLAGS=-mod=mod` makes the build write `go.sum` as it goes, and
`scripts/build.sh` skips tidy under it. The sandbox can never produce the
shipping `go.sum` (a mirror's module hashes differ from the proxy's), so
deliver without one when dependencies change and let `go mod tidy` on a real
machine write it.

## 3. Build and test

```bash
scripts/sandbox-floor.sh apply
scripts/build.sh --cross          # vet, race tests, build, all platforms
scripts/sandbox-floor.sh revert
./test.sh -F                      # the floor check, from a clean tree
```

A cold `build.sh` is about four minutes, nearly all of it the race build. The
race tests run because the sandbox has a C compiler.

**A test that fails intermittently elsewhere -- under `-race`, on a busier
machine -- will usually pass a single run here.** Reproduce it by volume
instead: compile the test binary once and run it hundreds of times, at several
CPU counts and with copies in parallel for load.

```bash
go test -race -c -o /tmp/pkg.test ./internal/fakedev
for cpu in 1 2 8; do /tmp/pkg.test -test.run TestName -test.count=400 -test.cpu=$cpu 2>&1 | grep -c -- '--- FAIL'; done
(for i in 1 2 3 4 5 6; do /tmp/pkg.test -test.run TestName -test.count=300 >/tmp/l$i.log 2>&1 & done; wait)
cat /tmp/l*.log | grep -c -- '--- FAIL'
```

A fix is proved by the same loops going to zero. fakedev's `DropAfter` race
was found this way: about 1 run in 8 here, invisible in any single run.

## 3a. The Qt application

```bash
scripts/sandbox-floor.sh apply
export GOTOOLCHAIN=local GOPROXY=direct GOSUMDB=off GOPRIVATE='*' GOFLAGS=-mod=mod
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j$(nproc) 2>&1 | grep -E 'error|warning:'
QT_QPA_PLATFORM=offscreen ./build/tests/replay_probe run.jsonl map.json /tmp/om 20
```

About half a minute from cold. **The export goes in the same command as
`cmake --build`**, every time: the Go archive is a custom command that
inherits the build's environment, and each tool call is a fresh shell.

**The stale-binary trap.** Build against a clean tree -- after a revert, or
without the export -- and the archive step fails with `go.mod requires go >=
1.25.0`, the build stops before anything C++ links, and every binary in
`build/` keeps its old contents and still runs. A change that seems to have
done nothing: check the binary's timestamp before the source.

**replay_probe** (docs/BUILDING.md) is the check: it plays a recording through
the real window offscreen and asserts the views against the run model, then
saves a grab per theme and, at a speed above zero, one from mid-run. **Look at
the grabs** with the image viewer, not just the exit status -- the probe
cannot see a pile of devices at the scene origin or a log scrolled sideways,
and both happened. Speed 0 finishes in one pull and never exercises the live
path; run it at 20 or 50 too.

Two things about offscreen grabs: `propagateSizeHints()` warnings are noise,
and `XDG_RUNTIME_DIR not set` is too.

**viewer_probe** drives the map viewer window, WebEngine included:

```bash
QTWEBENGINE_DISABLE_SANDBOX=1 QT_QPA_PLATFORM=offscreen ./build/tests/viewer_probe "" /tmp/omv
```

About ten seconds. WebEngine renders offscreen here -- the grabs have the
page in them -- so no Xvfb. The sandbox variable is because the tool shell is
root and Chromium refuses to run as root with its sandbox on; the GL and
`No suitable graphics backend` lines at startup are noise. Look at
`/tmp/omv-{light,dark,cyber}.png`: the probe checks colours on the page, not
the chrome around it, and an unthemed white menu bar on the dark theme got
past every check the first time.

The viewer's page is embedded in the Go archive, so an edit to
`internal/mapweb/assets` rebuilds the archive; if the viewer ever shows an old
page, check that the archive step ran (`Building Go c-archive`) before
suspecting the page. The viewer's Go side reads `$HOME` as the process started
-- Go copies the environment at load -- so a probe that wants its own layout
directory sets it on the window (`setLayoutDir`), not with `qputenv`.

**crawl_probe** runs a live crawl against the fake lab, and the sandbox can
host it: root is the default user, and loopback aliases work where dummy
interfaces do not.

```bash
DEBIAN_FRONTEND=noninteractive apt-get install -y iproute2   # for ip
./build/tests/fakelab -addrs | sh          # the lab's addresses, on lo
QT_QPA_PLATFORM=offscreen ./build/tests/crawl_probe /tmp/om-crawl
```

The addresses do not survive a sandbox reset; re-add them at the start of any
session that runs the probe or `TestCrawlTheFakeLab`. Both skip, and print
the commands, when they are missing. The crawl takes about three seconds with
the probe's 150ms per-command latency, which is there so the cancel test has a
crawl in flight to cancel.

What the fake lab found in its first run is the reason to keep running it.
Two bugs no unit test had seen: a first-contact host key filed against the
dialed address, which doubled every device reached by address into a phantom
failure, and failed devices dropped from the map with every link to them.

## 4. SNMP lab agent

```bash
mkdir -p /tmp/snmpd
cat > /tmp/snmpd/snmpd.conf <<'CONF'
agentAddress udp:127.0.0.1:16161
rocommunity pfsecret 127.0.0.1
sysName lab-snmpd.example
createUser pfv3 SHA "pfauth-secret" AES "pfpriv-secret"
rouser pfv3 priv
CONF
export MIBS=""
snmpd -C -c /tmp/snmpd/snmpd.conf -Lf /tmp/snmpd/log -p /tmp/snmpd/pid
PFSNMP_TEST_TARGET=127.0.0.1 PFSNMP_TEST_PORT=16161 PFSNMP_TEST_COMMUNITY=pfsecret \
    go test -count=1 ./internal/snmpprobe ./internal/crawldial -v -run 'Live|Learns'
```

Things that go wrong, none of them bugs:

- **The address goes in the config or on the command line, not both.** Given
  twice, snmpd binds once, fails the second bind, and exits with only a log
  line to say so.
- **snmpd is in /usr/sbin.** A `PATH` trimmed to pick a particular Go loses it.
- **Background processes do not survive between tool calls.** Start snmpd in
  the same command as the tests that use it; a test failing with "connection
  refused" on 16161 means it is gone.
- **`MIBS=""`** silences net-snmp's MIB-parsing warnings.
- **The CLIs have no SNMP port flag.** For `crawl -methods snmp` end to end, run
  a second config with `agentAddress udp:127.0.0.1:161`.
- **net-snmp has no LLDP-MIB or CDP-MIB.** Live runs prove transport,
  credentials and bindings; every device comes back with zero neighbors.

## 5. Vault and CLI end to end

No terminal and no keyring, so secrets are piped and the keyring is bypassed:

```bash
export OMEGAMAPS_VAULT_PASSWORD=lab-master-pw OMEGAMAPS_NO_KEYRING=1
V=/tmp/e2e/vault.json; mkdir -p /tmp/e2e
printf 'lab-master-pw\nlab-master-pw\n' | build/bin/omvault -vault $V init
echo pfsecret | build/bin/omvault -vault $V add -name lab-ro -auth snmp-v2c
build/bin/crawl -vault $V -methods snmp -seed 127.0.0.1 -depth 0 -snmp-timeout 1s -v
```

`omvault` reads one secret per line from stdin when there is no terminal. Run
the crawl twice: the second should say `(pinned)` and take a fraction of the
first's time. Use a throwaway `HOME` (`env -i HOME=$(mktemp -d) ...`) to
exercise the default `~/.omegamaps` path without touching anything real.

## 6. Terminals

The sandbox has no terminal, so anything that behaves differently on one --
password prompts, above all -- needs one simulated. `script` runs a command
under a real pseudo-terminal and forwards stdin to it:

```bash
printf 'lab-master-pw\n' | script -qec "build/bin/crawl -vault $V ... 2>/dev/null" /dev/null
```

That is how the prompt fix was proved: before it, a prompt written to stderr
was invisible with `2>/dev/null`; after it, the prompt goes to `/dev/tty` and
appears whatever stderr does.

## 7. The tool shell

The bash tool runs `/bin/sh`, which is dash: no `time` builtin (use
`date +%s` arithmetic) and no `<(...)` process substitution. Piping a command
into `grep` reports grep's exit status, not the command's -- capture `$?`
before filtering when the status is what you are checking.

`pkill -f <pattern>` matches the tool shell's own command line when the
pattern appears anywhere in the command, and kills it: the call returns with
no output at all. Kill by PID (`$!`, or a pid file), not by pattern. A
background process holding the shell's output open (a `sleep` feeding a fifo)
keeps the call from returning the same way.

A crash with no Go panic is native: `apt-get install -y gdb`, then
`gdb -batch -ex 'handle SIGPIPE SIGURG SIGUSR1 SIGUSR2 nostop noprint pass'
-ex run -ex bt --args <binary> ...`. The Go runtime's signals need the
`handle` line or gdb stops on them first.

## 8. Working from a recording

A crawl on real gear can be brought into the sandbox as its event stream:
`crawl -events run.jsonl` on the real network, then upload the file. It is
JSON Lines with a header line, so Python or `jq` reads it directly, and
`crawlrun.ReadEvents` / `crawlrun.Replay` rebuild the run model from it.
`crawlrun.Play` feeds it at its recorded pace, which is how the Qt views are
built and tested (section 3a). The file can be called anything; the one this
was built against arrived as `events.json`.

Two cautions from doing this. Per-device timings have to be taken from the
events that mark a device's own work (`phase`, `auth-ok`, `collect`), not from
`reached` alone -- a bug once held `reached` until a whole depth finished,
which made every device in the depth look slow. And a recording can only show
what the writer kept: check that a field is present in the file before
concluding the crawler never produced it (the credential was once dropped by
the writer, not the resolver).

## 9. Delivering

```bash
scripts/sandbox-floor.sh status      # must say clean
gofmt -l .                           # must print nothing
cd .. && zip -qr omegamaps.zip omegamaps \
    -x 'omegamaps/scripts/floor.local' 'omegamaps/scripts/.floor-backup/*' \
       'omegamaps/go.sum' 'omegamaps/build/*' 'omegamaps/map.json' '*.jsonl' \
       'omegamaps/.git/*'
```

`.git` is there when the session made a local repository to diff against the
upload; it is the sandbox's, not the project's.

Drop the `go.sum` exclusion when no module version changed: a revert restores
the shipping one byte for byte, and `cmp` against the one that arrived proves
it. Recordings and maps carry real device names; they stay out of the zip.

## 10. What the sandbox cannot tell you

- **No real LLDP or CDP.** Neighbor parsing is proved against fixtures; real
  walk captures from a QFX, an Arista and an IOS box are what will replace
  them.
- **No OS keyring.** `omvault keyring set` and keyring unlock are untested
  here.
- **No macOS or Windows.** `--cross` proves the binaries build and are the
  right format, not that they run; `scripts/build.bat` has never been executed
  here. The Qt application's MSVC seams and macOS framework links are not
  exercised at all.
- **No display.** The Qt application runs offscreen: grabs, layout and the
  run's data path are proved; input -- wheel zoom, drag, the file dialog --
  is not. The viewer's page takes synthesized clicks (viewer_probe clicks a
  real export button), but the native save and open dialogs are stood in for.
- **No WebEngine deploy.** The viewer runs from the build tree. Whether
  macdeployqt/windeployqt carry QtWebEngineProcess and its resources into a
  bundle is a real-machine question. Xvfb and xdotool would be the next level if one is ever needed.
- **Not the shipping toolchain.** Everything runs on the floor set with Go
  1.22; the 1.25 build with the real `go.sum` is only ever checked on a real
  machine.
