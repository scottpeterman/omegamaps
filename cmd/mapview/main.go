// cmd/mapview/main.go
// Harness for internal/mapweb: serve one map.json and open it.
//
// This is not a product surface. It exists so the map renders, the guards can
// be poked at with curl, and a click can be proven to arrive as a connect
// event — none of which needs a vault, a crawl or a window. When the map
// surface misbehaves inside the shell later, this is where to reproduce it
// without the shell in the way.
//
//	mapview -map lab-map.json
//	mapview -map lab-map.json -open=false     # print the URL, open it yourself
//	mapview -map lab-map.json -layouts ""     # no layout store: Save lasts a session
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/scottpeterman/omegamaps/internal/buildinfo"
	"github.com/scottpeterman/omegamaps/internal/mapweb"
	"github.com/scottpeterman/omegamaps/internal/vaultcli"
)

func main() {
	mapPath := flag.String("map", "", "path to a map.json to display (required)")
	open := flag.Bool("open", true, "open the viewer in the default browser")
	connect := flag.Bool("connect", false, "offer Connect on a node — only meaningful with -v, since this harness logs the click instead of opening a session")
	verbose := flag.Bool("v", false, "log refused requests")
	showVersion := flag.Bool("version", false, "print the version and exit")
	layouts := flag.String("layouts", defaultLayoutDir(), "directory for saved layouts, one file per map (\"\" keeps them in the browser, for this session only)")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Line("mapview"))
		return
	}

	if *mapPath == "" {
		fmt.Fprintln(os.Stderr, "mapview: -map is required")
		flag.Usage()
		os.Exit(2)
	}

	data, err := os.ReadFile(*mapPath)
	if err != nil {
		log.Fatalf("mapview: %v", err)
	}

	opts := mapweb.Options{}

	// The harness stands in for the shell's terminal launcher: it prints
	// what the application would have opened. That is the whole point of
	// running it — the click arriving here proves the wiring end to end
	// without a session on top of it.
	//
	// Off by default. The harness has no terminal to open, and a Connect
	// button that logs a line is worse than no button: it says the
	// application is there when it is not. -connect turns it on to prove
	// the wiring.
	if *connect {
		opts.OnConnect = func(n mapweb.NodeRef) {
			log.Printf("connect: %s ip=%s platform=%s discovered=%t",
				n.Name, n.IP, n.Platform, n.Discovered)
		}
	}
	if *verbose {
		opts.Log = log.Printf
	}

	srv, err := mapweb.Serve(opts)
	if err != nil {
		log.Fatalf("mapview: %v", err)
	}
	defer srv.Close()

	layoutPath := mapweb.LayoutPath(*layouts, *mapPath)
	if err := srv.SetMapLayout(filepath.Base(*mapPath), data, layoutPath); err != nil {
		log.Fatalf("mapview: %v", err)
	}
	if layoutPath != "" {
		log.Printf("mapview: layout saves to %s", layoutPath)
	}

	url := srv.URL()
	log.Printf("mapview: %d node(s) from %s", srv.NodeCount(), *mapPath)
	if !*connect {
		log.Print("mapview: read-only (use -connect to offer Connect on a node)")
	}
	log.Printf("mapview: %s", url)

	if *open {
		if err := openBrowser(url); err != nil {
			log.Printf("mapview: could not open a browser (%v) — use the URL above", err)
		}
	}

	// Ctrl-C rather than a timeout: the server is the session.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Print("mapview: stopping")
}

// openBrowser hands a URL to the desktop. It lives here rather than in
// internal/mapweb because "which browser" is a property of the front end: the
// GUI will use the toolkit's own opener, and the package must not care.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// defaultLayoutDir is where the application keeps layouts, so a layout saved
// here is the one the viewer window restores, and the other way round.
func defaultLayoutDir() string {
	if d := vaultcli.AppDir(); d != "" {
		return filepath.Join(d, "layouts")
	}
	return ""
}
