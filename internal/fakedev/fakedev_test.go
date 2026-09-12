// internal/fakedev/fakedev_test.go
//
// Tests of the fake itself. A fixture that lies is worse than no fixture,
// because everything above it then passes for the wrong reason — so the
// behaviors other packages will lean on are pinned here first.
package fakedev_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/fakedev"
	"github.com/scottpeterman/omegamaps/internal/netexec"
)

func open(t *testing.T, cfg fakedev.Config, opt netexec.Options) (*fakedev.Server, *netexec.Session) {
	t.Helper()
	srv, err := fakedev.Start(cfg)
	if err != nil {
		t.Fatalf("start device: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	client, err := srv.Dial("lab", "lab")
	if err != nil {
		t.Fatalf("dial device: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	if opt.CommandTimeout == 0 {
		opt.CommandTimeout = 5 * time.Second
	}
	sess, err := netexec.Open(context.Background(), client, opt)
	if err != nil {
		t.Fatalf("open shell: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return srv, sess
}

func TestRunReturnsCommandOutputWithoutEchoOrPrompt(t *testing.T) {
	_, sess := open(t, fakedev.IOS("lab-r1"), netexec.Options{PagingDisable: "terminal length 0"})

	out, err := sess.Run(context.Background(), "show version")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "Cisco IOS Software") {
		t.Errorf("version output missing, got %q", out)
	}
	if strings.Contains(out, "show version") {
		t.Errorf("command echo was not stripped: %q", out)
	}
	if strings.Contains(out, "lab-r1#") {
		t.Errorf("prompt was not stripped: %q", out)
	}
}

func TestPromptCarriesTheDeviceName(t *testing.T) {
	_, sess := open(t, fakedev.IOS("lab-r1"), netexec.Options{})

	if got := sess.Prompt(); got != "lab-r1#" {
		t.Errorf("Prompt() = %q, want %q", got, "lab-r1#")
	}
}

func TestUnknownCommandLooksLikeARejection(t *testing.T) {
	_, sess := open(t, fakedev.IOS("lab-r1"), netexec.Options{})

	out, err := sess.Run(context.Background(), "set cli screen-length 0")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// netexec keeps its CLI-error matcher unexported, so assert on the
	// text it matches rather than reaching into the package.
	if !strings.Contains(out, "%") {
		t.Errorf("rejection did not look like one: %q", out)
	}
}

func TestAskedRecordsExactlyWhatWentOnTheWire(t *testing.T) {
	srv, sess := open(t, fakedev.IOS("lab-r1"), netexec.Options{PagingDisable: "terminal length 0"})

	if _, err := sess.Run(context.Background(), "show version"); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"terminal length 0", "show version"}
	got := srv.Asked()
	if len(got) != len(want) {
		t.Fatalf("Asked() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Asked()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestChunkedOutputStillMatchesThePrompt(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.ChunkSize = 7
	cfg.ChunkDelay = time.Millisecond
	_, sess := open(t, cfg, netexec.Options{PagingDisable: "terminal length 0"})

	out, err := sess.Run(context.Background(), "show running-config")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "router bgp 65001") {
		t.Errorf("chunked output lost content: %q", out)
	}
}

func TestHangingCommandTimesOutRatherThanBlockingForever(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.Hang = []string{"show tech-support"}
	_, sess := open(t, cfg, netexec.Options{
		PagingDisable:  "terminal length 0",
		CommandTimeout: 250 * time.Millisecond,
	})

	start := time.Now()
	_, err := sess.Run(context.Background(), "show tech-support")
	if err == nil {
		t.Fatal("wedged command returned no error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("want a timeout error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took %s; the bound is not being honored", elapsed)
	}
}

func TestDroppedConnectionIsReportedAsClosedNotAsTimeout(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.DropAfter = 1 // the paging command is the first, so the next Run dies
	_, sess := open(t, cfg, netexec.Options{
		PagingDisable:  "terminal length 0",
		CommandTimeout: 5 * time.Second,
	})

	_, err := sess.Run(context.Background(), "show version")
	if err == nil {
		t.Fatal("run after a drop returned no error")
	}
	if strings.Contains(err.Error(), "timeout") {
		t.Errorf("a dropped session was reported as a timeout: %v", err)
	}
}

// A device answering with a running-config rather than a neighbor list is
// two orders of magnitude more output. The limit is now stated explicitly
// rather than left to the default: this test is about reassembly, and if it
// ever depended on whatever DefaultMaxOutputBytes happens to be, changing
// that default would move this test's meaning without touching it.
func TestLargeOutputIsReturnedWhole(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.Flood = map[string]int{"show running-config all": 512 * 1024}
	_, sess := open(t, cfg, netexec.Options{
		PagingDisable:  "terminal length 0",
		CommandTimeout: 20 * time.Second,
		MaxOutputBytes: 4 << 20,
	})

	out, err := sess.Run(context.Background(), "show running-config all")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Count lines, not bytes: the device sends CRLF and netexec
	// normalizes to LF, so a byte total would be off by one per line and
	// the assertion would have to be fuzzy to pass.
	got := strings.Count(out, fakedev.FloodLine)
	if want := fakedev.FloodLines(512 * 1024); got != want {
		t.Errorf("large output truncated: got %d lines, want %d", got, want)
	}
}

// The limit fires, and it fires as an error rather than as a short string.
// A truncated running-config is the dangerous outcome here: it parses, it
// diffs, it looks like a capture, and the missing half is discovered the
// day someone restores from it by hand.
func TestOutputPastTheLimitIsAnErrorNotATruncation(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.Flood = map[string]int{"show running-config all": 512 * 1024}
	_, sess := open(t, cfg, netexec.Options{
		PagingDisable:  "terminal length 0",
		CommandTimeout: 20 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})

	out, err := sess.Run(context.Background(), "show running-config all")
	if !errors.Is(err, netexec.ErrOutputTooLarge) {
		t.Fatalf("Run err = %v, want ErrOutputTooLarge", err)
	}
	if out != "" {
		t.Errorf("over-limit output was returned anyway (%d bytes); it must not look like a capture", len(out))
	}
	if !strings.Contains(err.Error(), "limit 65536") {
		t.Errorf("error does not name the limit that fired: %v", err)
	}
}

// Blowing the limit must not cost the session. The read keeps draining and
// the prompt is still matched, so the device is never left blocked on a
// full window with nobody reading — the "never hang a device" rule, at the
// one size where it actually bites.
func TestSessionStillUsableAfterAnOverLimitCommand(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.Flood = map[string]int{"show running-config all": 512 * 1024}
	_, sess := open(t, cfg, netexec.Options{
		PagingDisable:  "terminal length 0",
		CommandTimeout: 20 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})

	if _, err := sess.Run(context.Background(), "show running-config all"); !errors.Is(err, netexec.ErrOutputTooLarge) {
		t.Fatalf("setup: want ErrOutputTooLarge, got %v", err)
	}
	out, err := sess.Run(context.Background(), "show version")
	if err != nil {
		t.Fatalf("session unusable after an over-limit command: %v", err)
	}
	if !strings.Contains(out, "Cisco IOS Software") {
		t.Errorf("next command returned the wrong thing: %q", out)
	}
}

// A negative limit means the caller has taken responsibility. Kept because
// reach is a one-device tool that owns the machine it runs on, and there is
// no good answer to "how big is a show tech" to hard-code.
func TestNegativeLimitDisablesTheBound(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.Flood = map[string]int{"show running-config all": 512 * 1024}
	_, sess := open(t, cfg, netexec.Options{
		PagingDisable:  "terminal length 0",
		CommandTimeout: 20 * time.Second,
		MaxOutputBytes: -1,
	})

	out, err := sess.Run(context.Background(), "show running-config all")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := strings.Count(out, fakedev.FloodLine), fakedev.FloodLines(512*1024); got != want {
		t.Errorf("got %d lines, want %d", got, want)
	}
}

// Cancel has to reach a command already in flight. The distinguishing
// evidence is the clock: a wedged command with a 30s CommandTimeout would
// return in 30s whether or not the context did anything, so the assertion
// is that it returns in well under that.
func TestCancelReachesACommandInFlight(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.Commands["show tech-support"] = "starting"
	cfg.Hang = []string{"show tech-support"}
	_, sess := open(t, cfg, netexec.Options{
		PagingDisable:  "terminal length 0",
		CommandTimeout: 30 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := sess.Run(ctx, "show tech-support")
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("cancel took %s; the command timeout ran instead of the context", elapsed)
	}
}

// The cheap half of the same property: a context already cancelled must not
// put the command on the wire at all. Asked() is the check with teeth —
// it reports what the device actually received.
func TestCancelBeforeSendNeverTouchesTheDevice(t *testing.T) {
	srv, sess := open(t, fakedev.IOS("lab-r1"), netexec.Options{PagingDisable: "terminal length 0"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := sess.Run(ctx, "show running-config"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	// Asked() is recorded by the device's shell goroutine, so a command
	// that WAS sent needs a moment to show up. Without this pause the
	// assertion passes whether or not the write happened, which is the
	// difference between a test and a decoration.
	time.Sleep(250 * time.Millisecond)
	for _, cmd := range srv.Asked() {
		if cmd == "show running-config" {
			t.Fatal("a cancelled command was still sent to the device")
		}
	}
}

func TestNoEchoDeviceStillYieldsCleanOutput(t *testing.T) {
	cfg := fakedev.IOS("lab-r1")
	cfg.NoEcho = true
	_, sess := open(t, cfg, netexec.Options{PagingDisable: "terminal length 0"})

	out, err := sess.Run(context.Background(), "show version")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "Cisco IOS Software") {
		t.Errorf("output missing on a non-echoing device: %q", out)
	}
}

func TestSessionsCountsLogins(t *testing.T) {
	srv, _ := open(t, fakedev.IOS("lab-r1"), netexec.Options{})
	if got := srv.Sessions(); got != 1 {
		t.Errorf("Sessions() = %d, want 1", got)
	}
}

func TestBadPasswordIsRejected(t *testing.T) {
	srv, err := fakedev.Start(fakedev.Config{
		Prompt:   "lab-r1#",
		Username: "lab",
		Password: "correct",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Close()

	if _, err := srv.Dial("lab", "wrong"); err == nil {
		t.Fatal("wrong password was accepted")
	}
}
