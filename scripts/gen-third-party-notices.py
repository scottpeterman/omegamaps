#!/usr/bin/env python3
"""gen-third-party-notices.py -- regenerate licenses/THIRD_PARTY_NOTICES.md.

    RUN FROM: the repo root (the directory holding go.mod)
    WRITES:   licenses/THIRD_PARTY_NOTICES.md

BSD-3-Clause, BSD-2-Clause, MIT and Apache-2.0 all require the copyright notice
and the warranty disclaimer to be reproduced in the materials distributed with
a binary. omegamaps links every Go dependency below into a static archive and
embeds the JavaScript below into that archive with //go:embed, so the binary
carries their code and the obligation travels with it.

WHY THIS IS GENERATED AND NOT WRITTEN BY HAND. A notices file maintained by
hand is a notices file that is correct on the day it is written. Dependencies
get added, versions move, and a module that changes its licence between
releases does so silently. This reads the LICENSE file that shipped inside each
module in the local cache, so what lands in the output is the text the build
actually consumed rather than a licence somebody remembered.

    python3 scripts/gen-third-party-notices.py

WHAT IT ASKS GO FOR, AND WHAT IT DOES NOT. The question is "whose code is in
the binaries", and the answer is `go list -deps` over ./capi AND ./cmd/... --
NOT `go list -m all`, which walks the whole module GRAPH and would claim
obligations for test-only dependencies of dependencies whose code is never
linked.

    TWO ROOTS, NOT ONE, and this is where the omegassh version of this script
    would have been wrong here. omegassh shipped one artifact, so ./capi was
    the whole answer. omegamaps also ships crawl, mapview and omvault out of
    cmd/, and internal/vaultcli -- reached only from cmd/omvault -- is the sole
    importer of golang.org/x/term and github.com/zalando/go-keyring. Asking
    only about ./capi produces a notices file that is correct for the Qt
    application and silently short for the CLI sitting beside it in build/bin.

CANONICAL PATHS, CACHED FILES. `go list` is asked for .Path but for
.Replace.Dir when a replace directive is in force, so a sandbox that mirrors
golang.org/x/crypto through github.com/golang/crypto still reports the module
under its real name while reading the files that were really used.

HAND TABLES, AND WHY EACH ONE IS A TABLE. Three kinds of component do not come
through the module cache, so there is nothing for the generator to read:

  NATIVE   -- Qt and Qt WebEngine, resolved by CMake against whatever the
              platform provides. Nothing on disk to read, and for Qt the
              linkage is the fact the licence turns on, so it is stated.
  VENDORED -- the JavaScript under internal/mapweb/assets/vendor/, embedded
              into the binary. Licence text IS read from a file, but from a
              sidecar under licenses/vendor/ rather than from the asset: the
              webpack build of cytoscape-dagre strips the banner comment, so
              the copy in this tree carries no notice of its own.
  BUNDLED  -- the TextFSM templates, which are data rather than code but are
              embedded and distributed all the same.
"""

import os
import subprocess
import sys

# Beside the licence texts it points at, not at the repo root. Both the path
# and the exact spelling have to match what is committed: this script truncates
# and rewrites OUT, so a mismatch does not fail -- it writes a SECOND file and
# leaves the committed one to go stale in place, still looking maintained.
#
# Every `licences/...` path in the notes below is therefore relative to this
# directory rather than to the repo root.
OUT = os.path.join("licenses", "THIRD_PARTY_NOTICES.md")
PROJECT = "omegamaps"

# ---------------------------------------------------------------------------
# ANSWER THIS BEFORE THE FIRST PUBLIC PUSH.
#
# internal/tfsm/templates/ holds 19 TextFSM templates named to exactly the
# ntc-templates convention -- cisco_ios_show_cdp_neighbors_detail.textfsm and
# so on. ntc-templates is Apache-2.0, which requires attribution and a
# statement of changes. A reviewer who sees that naming will assume derivation
# whether or not it happened, so the file has to say which it is.
#
# Set this to "original" or "ntc-templates". The script refuses to write a
# notices file while it is None, because the wrong answer here is the kind
# that is only discovered by somebody else.
TEMPLATES_PROVENANCE = None
# ---------------------------------------------------------------------------

TEMPLATES_NOTE = {
    "original": (
        "Embedded from `internal/tfsm/templates/` via //go:embed. Written for "
        "this project against live device output, not derived from another "
        "template collection. The filenames follow the "
        "`<vendor>_<os>_<command>.textfsm` convention that ntc-templates also "
        "uses, because it is the convention the format grew up with -- noted "
        "here so the resemblance is not mistaken for provenance."
    ),
    "ntc-templates": (
        "Embedded from `internal/tfsm/templates/` via //go:embed. Derived from "
        "ntc-templates (https://github.com/networktocode/ntc-templates), "
        "Apache-2.0, Copyright Network to Code LLC. Modified for this project: "
        "the templates have been adapted to the platforms and command output "
        "this crawler collects. Full licence text: "
        "`Apache-2.0.txt`, beside this file."
    ),
}

# Components that are not Go modules and have no readable licence file.
# Reviewed by hand; see the module docstring.
NATIVE = [
    {
        "name": "Qt 6",
        "holder": "The Qt Company Ltd and other contributors",
        "licence": "GNU Lesser General Public License v3.0",
        "url": "https://www.qt.io/",
        "note": (
            "Dynamically linked against the unmodified Qt shared libraries "
            "provided by the platform. omegamaps is conveyed under GPLv3, "
            "which LGPLv3 section 3 expressly permits. Full licence text: "
            "`LGPL-3.0.txt` beside this file, together with `GPL-3.0.txt` "
            "which it incorporates by reference."
        ),
    },
    {
        "name": "Qt WebEngine",
        "holder": "The Qt Company Ltd, The Chromium Authors and others",
        "licence": "GNU Lesser General Public License v3.0 (Chromium: "
                   "BSD-3-Clause and others)",
        "url": "https://doc.qt.io/qt-6/qtwebengine-licensing.html",
        "note": (
            "Dynamically linked, and used only by the map viewer "
            "(`viewer/`); the application runs without it. Qt WebEngine "
            "embeds Chromium, whose own third-party component list runs to "
            "several thousand entries under a range of permissive licences. "
            "That list is published and maintained by The Qt Company at the "
            "URL above and is not reproduced here -- a copy would be stale "
            "within one Qt release, and a stale component list is worse than "
            "a pointer to the authoritative one. The Chromium notices also "
            "ship inside the Qt installation, under "
            "`<qt-prefix>/licenses/qtwebengine/`."
        ),
    },
    {
        "name": "Go standard library and runtime",
        "holder": "The Go Authors",
        "licence": "BSD-3-Clause",
        "url": "https://go.dev/",
        "note": (
            "Statically linked into the c-archive and into each command under "
            "`cmd/`, along with the modules below. Listed by hand because the "
            "runtime reports no module in `go list`, so nothing in the "
            "generated section can find it."
        ),
    },
]

# JavaScript embedded into the binary by //go:embed assets in
# internal/mapweb/server.go, and served to the map view. "licence_file" is
# read from disk so the text is the real one rather than a remembered one; see
# the module docstring for why it is a sidecar and not the asset itself.
VENDORED = [
    {
        "name": "Cytoscape.js",
        "holder": "The Cytoscape Consortium",
        "licence": "MIT",
        "url": "https://js.cytoscape.org/",
        "asset": "internal/mapweb/assets/vendor/cytoscape.min.js",
        "licence_file": "licenses/vendor/cytoscape.LICENSE",
        "version": None,
        "note": "The graph rendering engine behind the topology view.",
    },
    {
        "name": "dagre",
        "holder": "Chris Pettitt",
        "licence": "MIT",
        "url": "https://github.com/dagrejs/dagre",
        "asset": "internal/mapweb/assets/vendor/dagre.min.js",
        "licence_file": "licenses/vendor/dagre.LICENSE",
        "version": None,
        "note": "Directed-graph layout, used for the hierarchical arrangement.",
    },
    {
        "name": "cytoscape.js-dagre",
        "holder": "The Cytoscape Consortium",
        "licence": "MIT",
        "url": "https://github.com/cytoscape/cytoscape.js-dagre",
        "asset": "internal/mapweb/assets/vendor/cytoscape-dagre.js",
        "licence_file": "licenses/vendor/cytoscape-dagre.LICENSE",
        "version": None,
        "note": (
            "Adapter binding dagre's layout to Cytoscape. THE COPY IN THIS "
            "TREE CARRIES NO LICENCE HEADER -- the webpack build strips the "
            "banner comment -- so the text below was taken from the upstream "
            "LICENSE file and is kept in `vendor/` beside this file."
        ),
    },
]

# Every platform omegamaps ships for. The notices file has to cover all of
# them, not whichever one happens to be running this script.
TARGETS = [("linux", "amd64"), ("windows", "amd64"), ("darwin", "arm64")]

# The archive plus every command that lands in build/bin. See the docstring.
ROOTS = ["./capi", "./cmd/..."]


def go_modules():
    """Every module whose code is compiled into any shipped artifact.

    ONCE PER GOOS, UNIONED. Build constraints decide what gets linked, and the
    keyring backends are split three ways: github.com/danieljoos/wincred is
    Windows-only and github.com/godbus/dbus/v5 is Linux-only. A run on one
    platform therefore produces a notices file that is silently short for the
    binaries shipped for the others.
    """
    fmt = ("{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}\t"
           "{{if .Module.Replace}}{{.Module.Replace.Dir}}"
           "{{else}}{{.Module.Dir}}{{end}}{{end}}")
    seen_raw = []
    for goos, goarch in TARGETS:
        # CGO_ENABLED=1, matching the real build. It matters: c-archive
        # requires cgo, and turning it off flips build constraints -- with
        # CGO_ENABLED=0 the keyring backends disappear. `go list` resolves
        # rather than compiles, so no cross toolchain is needed to ask.
        env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="1")
        try:
            seen_raw.append(subprocess.check_output(
                ["go", "list", "-deps", "-f", fmt] + ROOTS,
                text=True, env=env))
        except FileNotFoundError:
            sys.exit("go not on PATH -- this needs the toolchain that built "
                     "the archive, because it reads that build's module cache")
        except subprocess.CalledProcessError as e:
            sys.exit("go list failed for %s/%s (%s). Run `go mod download` "
                     "first." % (goos, goarch, e))
    raw = "\n".join(seen_raw)

    seen = {}
    for line in raw.splitlines():
        parts = line.split("\t")
        if len(parts) != 3:
            continue
        path, version, directory = parts
        # The standard library reports no module; the main module reports no
        # version. Neither is a third-party notice: Go's own licence covers the
        # first (recorded in NATIVE) and LICENSE covers the second.
        if not path or not version or not directory:
            continue
        seen[path] = (version, directory)
    return sorted((p, v, d) for p, (v, d) in seen.items())


def licence_text(directory):
    """The licence file that shipped inside the module, verbatim."""
    for name in ("LICENSE", "LICENSE.txt", "LICENSE.md", "LICENCE",
                 "COPYING", "COPYING.txt"):
        candidate = os.path.join(directory, name)
        if os.path.isfile(candidate):
            with open(candidate, encoding="utf-8", errors="replace") as fh:
                return name, fh.read().rstrip()
    return None, None


def read_sidecar(path):
    if not os.path.isfile(path):
        return None
    with open(path, encoding="utf-8", errors="replace") as fh:
        return fh.read().rstrip()


def check_vendored():
    """Every vendored asset must exist and have a licence sidecar.

    BOTH DIRECTIONS ARE CHECKED. A sidecar with no asset is a dependency that
    was removed and left a notice claiming it is still shipped; an asset with
    no sidecar is the opposite and the one that matters legally. Neither is
    something to discover from a downstream packager.
    """
    problems = []
    declared = set()
    for item in VENDORED:
        declared.add(os.path.normpath(item["asset"]))
        if not os.path.isfile(item["asset"]):
            problems.append("declared but not present: %s" % item["asset"])
        if not os.path.isfile(item["licence_file"]):
            problems.append("no licence sidecar for %s -- expected %s"
                            % (item["name"], item["licence_file"]))

    vendor_dir = os.path.join("internal", "mapweb", "assets", "vendor")
    if os.path.isdir(vendor_dir):
        for entry in sorted(os.listdir(vendor_dir)):
            if not entry.endswith((".js", ".css")):
                continue
            full = os.path.normpath(os.path.join(vendor_dir, entry))
            if full not in declared:
                problems.append(
                    "%s is embedded but not declared in VENDORED -- it ships "
                    "inside the binary with no notice" % full)
    return problems


def main():
    if not os.path.isfile("go.mod"):
        sys.exit("run this from the repo root -- go.mod not found")

    if TEMPLATES_PROVENANCE not in TEMPLATES_NOTE:
        sys.exit(
            "TEMPLATES_PROVENANCE is not set.\n"
            "  internal/tfsm/templates/ is embedded and distributed. Set the\n"
            "  constant near the top of this script to \"original\" or to\n"
            "  \"ntc-templates\" -- see the comment there for why the\n"
            "  filenames alone do not settle it.")

    problems = check_vendored()
    if problems:
        sys.stderr.write("vendored JavaScript is not in a shippable state:\n")
        for p in problems:
            sys.stderr.write("  - %s\n" % p)
        return 1

    mods = go_modules()
    missing = []
    chunks = []

    for path, version, directory in mods:
        name, text = licence_text(directory)
        if text is None:
            # Reported rather than skipped. A dependency whose licence cannot
            # be found is the one that most needs a human to look at it.
            missing.append(path)
            continue
        chunks.append(
            "### %s\n\n"
            "Version %s. Licence text below is `%s` as shipped in the module.\n\n"
            "```\n%s\n```\n" % (path, version, name, text))

    with open(OUT, "w", encoding="utf-8") as out:
        out.write(
            "# Third-party notices\n\n"
            "%s is licensed under the GNU General Public License v3.0; see\n"
            "`LICENSE` at the repository root. It incorporates the components\n"
            "below, whose licences\n"
            "require their copyright notices and warranty disclaimers to be\n"
            "reproduced in the materials distributed with a binary.\n\n"
            "**This file is generated.** Run\n"
            "`python3 scripts/gen-third-party-notices.py` after changing\n"
            "dependencies; do not edit it by hand.\n\n"
            "## Native components\n\n" % PROJECT)

        for item in NATIVE:
            out.write("### %s\n\n- Copyright %s\n- %s\n- %s\n\n%s\n\n" % (
                item["name"], item["holder"], item["licence"], item["url"],
                item["note"]))

        out.write(
            "## Embedded web assets\n\n"
            "Compiled into the binary by `//go:embed` in "
            "`internal/mapweb/server.go` and served to the map view.\n\n")

        for item in VENDORED:
            version = item["version"] or "version not recorded in the asset"
            out.write(
                "### %s\n\n- Copyright %s\n- %s\n- %s\n- `%s` (%s)\n\n%s\n\n"
                "```\n%s\n```\n\n" % (
                    item["name"], item["holder"], item["licence"], item["url"],
                    item["asset"], version, item["note"],
                    read_sidecar(item["licence_file"])))

        out.write("## Embedded data\n\n")
        out.write("### TextFSM templates\n\n%s\n\n"
                  % TEMPLATES_NOTE[TEMPLATES_PROVENANCE])

        out.write("## Go modules\n\n"
                  "Statically linked into the transport archive and into the "
                  "commands under `cmd/`.\n\n")
        out.write("\n".join(chunks))

        if missing:
            out.write("\n## Licence text not found\n\n"
                      "These modules are linked but no licence file was found "
                      "in their cache directory. Resolve before distributing.\n\n")
            for path in missing:
                out.write("- %s\n" % path)

    print("wrote %s -- %d Go modules, %d native, %d embedded assets"
          % (OUT, len(chunks), len(NATIVE), len(VENDORED)))
    if missing:
        print("WARNING: no licence text found for: %s" % ", ".join(missing))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())