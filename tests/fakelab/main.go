// tests/fakelab/main.go
//
// Runs the fake lab (internal/fakedev/lab.go) until told to stop, for probes
// that are not Go -- crawl_probe drives a real crawl through the C surface
// and the window against it. Not a tool: it lives under tests/ so the build
// scripts never ship it.
//
//	fakelab           start the lab; print one "ready" line of JSON on stdout;
//	                  run until stdin closes or a signal arrives
//	fakelab -latency 200ms   the same, every command answered that much later
//	fakelab -addrs    print the commands that give this host the lab's
//	                  addresses, and exit
//
// The devices listen on port 22 at the lab's addresses, so this needs those
// addresses on the host (see -addrs) and the right to bind port 22. When it
// cannot start, it says so on stderr and exits 3, which a probe reports as a
// skip rather than a failure.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/scottpeterman/omegamaps/internal/fakedev"
)

type ready struct {
	Ready    bool     `json:"ready"`
	Seed     string   `json:"seed"`
	User     string   `json:"user"`
	Password string   `json:"password"`
	Devices  []device `json:"devices"`
}

type device struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
}

func main() {
	addrs := flag.Bool("addrs", false, "print the commands that give this host the lab's addresses")
	latency := flag.Duration("latency", 0, "delay every command's output by this much")
	flag.Parse()
	if *addrs {
		fmt.Println(strings.Join(fakedev.LabAddrCommands(), "\n"))
		return
	}

	lab, err := fakedev.StartLab(*latency)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakelab: %v\n", err)
		fmt.Fprintf(os.Stderr, "fakelab: the lab needs these addresses and the right to bind port 22:\n  %s\n",
			strings.Join(fakedev.LabAddrCommands(), "\n  "))
		os.Exit(3)
	}
	defer lab.Close()

	r := ready{Ready: true, Seed: fakedev.LabSeed, User: fakedev.LabUser, Password: fakedev.LabPassword}
	for _, d := range lab.Devices {
		r.Devices = append(r.Devices, device{Name: d.Name, Addr: d.Addr})
	}
	line, _ := json.Marshal(r)
	fmt.Println(string(line))

	// Whichever comes first: the parent closing our stdin (it exited, or it
	// is done with us) or a signal.
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(done)
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
	case <-sig:
	}
}
