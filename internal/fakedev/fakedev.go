// internal/fakedev/fakedev.go
//
// A real SSH server, in process, that behaves like a network device: it
// answers auth, grants a PTY, opens a shell, echoes what you type, and
// replies to commands from a table.
//
// This exists because the interesting failures in the exec path are all
// wire-level. A command echo that arrives split across two reads, a prompt
// that never comes back, output that arrives faster than anything drains
// it, a paging command the device rejects — none of those are visible to a
// fake that records method calls. Everything above this package (sshcore,
// netexec, and whatever capture becomes) runs against it unmodified, so a
// test drives the same code path a lab device does.
//
// What it cannot do is tell you the field knowledge is right. A fixture
// says what we believe a device says. Only the device says what it says.
// Use this to prove the engine is correct, and real gear to prove the
// personalities are.
//
// Deliberately no dependency on testing: this is an ordinary package, so
// nothing here registers test flags or drags the testing package into a
// binary. Callers own the lifetime — Start, defer Close.
package fakedev

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/scottpeterman/omegamaps/internal/sshcore"
)

// Config describes one device. The zero value is a well-behaved device with
// no commands: every knob below defaults to "nothing unusual happens", so a
// test opts into each misbehavior by name and the reader of that test can
// see exactly which one is under examination.
type Config struct {
	// Prompt is written after the banner and after every command, e.g.
	// "lab-r1#". Required — a device with no prompt would hang every
	// caller, and if that is what a test wants, Hang is the honest way
	// to ask for it.
	Prompt string

	// Banner is written once before the first prompt (MOTD, legal
	// notice). Lines are sent CRLF-terminated as a real device does.
	Banner string

	// Username and Password gate the connection. Empty Password with
	// AcceptAnyPassword accepts anything, which is the usual case —
	// most tests are not about auth.
	Username          string
	Password          string
	AcceptAnyPassword bool

	// Commands maps an exact command string to its output. A command
	// present here with empty output is accepted silently, which is what
	// a paging-disable command does on real gear.
	Commands map[string]string

	// Unknown is the reply to a command not in Commands. It should look
	// like the platform's real rejection, because netexec.isCLIError is
	// what reads it and platform detection turns on that answer.
	Unknown string

	// Latency delays every command's output. Models a loaded control
	// plane; also makes timeout tests deterministic without sleeping in
	// the test itself.
	Latency time.Duration

	// ChunkSize and ChunkDelay split output into pieces written with a
	// pause between them. A single Write is one arrival to the reader;
	// real output arrives in many, and prompt matching that works on one
	// arrival can fail across several.
	ChunkSize  int
	ChunkDelay time.Duration

	// Hang lists commands that emit their output and then never return a
	// prompt. The session stays open and readable — this is a wedged
	// command, not a dropped link, and the two look nothing alike to the
	// code under test.
	Hang []string

	// Flood maps a command to a number of bytes to emit before the
	// prompt. For exercising what happens to an unbounded read buffer
	// when a device answers with a running-config rather than an
	// interface list.
	Flood map[string]int

	// DropAfter answers this many commands and then closes the connection
	// when the next one arrives, without answering it. Zero never drops.
	// Models a device that resets mid-capture.
	DropAfter int

	// Listen is the address to accept on. Empty is a loopback port the OS
	// picks, which is what a test wants. A crawl that follows neighbors
	// needs devices at the addresses their neighbors report -- the crawler
	// refuses loopback addresses as neighbors on purpose -- so a fake lab
	// sets this to an address like "172.16.1.2:22" that the host has been
	// given. Binding port 22 needs the privileges that implies.
	Listen string

	// NoEcho suppresses character echo. Real gear with a PTY echoes, and
	// netexec.StripEchoAndPrompt depends on it, so the default is on;
	// this exists to prove the strip is not load-bearing for
	// correctness.
	NoEcho bool
}

// Server is a running fake device.
type Server struct {
	cfg  Config
	ln   net.Listener
	host string
	port int

	mu       sync.Mutex
	asked    []string
	sessions int
	closed   bool
}

// Start brings up a device on a loopback port chosen by the OS.
func Start(cfg Config) (*Server, error) {
	if strings.TrimSpace(cfg.Prompt) == "" {
		return nil, fmt.Errorf("fakedev: Prompt is required")
	}
	if cfg.Unknown == "" {
		cfg.Unknown = "% Invalid input detected at '^' marker."
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("fakedev: generate host key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("fakedev: host signer: %w", err)
	}

	sc := &ssh.ServerConfig{
		PasswordCallback: func(m ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if cfg.Username != "" && m.User() != cfg.Username {
				return nil, fmt.Errorf("unknown user")
			}
			if cfg.AcceptAnyPassword || cfg.Password == "" {
				return nil, nil
			}
			if string(pw) != cfg.Password {
				return nil, fmt.Errorf("bad password")
			}
			return nil, nil
		},
	}
	sc.AddHostKey(signer)

	addr := cfg.Listen
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("fakedev: listen: %w", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	s := &Server{cfg: cfg, ln: ln, host: host, port: port}
	go s.serve(sc)
	return s, nil
}

// Addr is the host:port the device is listening on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Host and Port are the same address split, for building an sshcore.Config.
func (s *Server) Host() string { return s.host }
func (s *Server) Port() int    { return s.port }

// Close stops the device.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.ln.Close()
}

// Asked returns every command the device was sent, in order, across all
// sessions.
//
// This is the read-only check with teeth. An allowlist asserts that the
// commands we wrote down are safe; this asserts that the commands actually
// put on the wire are the ones we wrote down — which is the property that
// matters and the only one a caller can break by accident.
func (s *Server) Asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.asked))
	copy(out, s.asked)
	return out
}

// Sessions reports how many shells have been opened against the device.
// A capture that logs in twice per device costs twice the auth and shows
// up here as a number, not as a slow run nobody explains.
func (s *Server) Sessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions
}

// Dial connects through sshcore, so callers exercise the real dial path
// rather than building an ssh.Client by hand.
//
// HostKeyInsecure is correct here and only here: the device generates a
// throwaway host key at Start, so there is nothing a known_hosts file could
// be checked against. This is the lab exception, not a general one.
func (s *Server) Dial(user, password string) (*sshcore.Client, error) {
	return sshcore.Dial(sshcore.Config{
		Host:     s.host,
		Port:     s.port,
		Username: user,
		Password: password,
		HostKeys: sshcore.HostKeyInsecure,
	})
}

func (s *Server) serve(sc *ssh.ServerConfig) {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(nc, sc)
	}
}

func (s *Server) handleConn(nc net.Conn, sc *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, sc)
	if err != nil {
		nc.Close()
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "only sessions here")
			continue
		}
		ch, chReqs, err := nch.Accept()
		if err != nil {
			return
		}
		go s.handleSession(ch, chReqs, conn)
	}
}

func (s *Server) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, conn ssh.Conn) {
	for req := range reqs {
		switch req.Type {
		case "pty-req", "window-change", "env":
			req.Reply(true, nil)
		case "shell":
			req.Reply(true, nil)
			s.mu.Lock()
			s.sessions++
			s.mu.Unlock()
			go s.runShell(ch, conn)
		default:
			req.Reply(false, nil)
		}
	}
}

// runShell is the device's CLI: echo input, and on a line break answer the
// accumulated command.
func (s *Server) runShell(ch ssh.Channel, conn ssh.Conn) {
	defer ch.Close()

	if s.cfg.Banner != "" {
		for _, line := range strings.Split(strings.TrimRight(s.cfg.Banner, "\n"), "\n") {
			io.WriteString(ch, line+"\r\n")
		}
	}
	io.WriteString(ch, s.cfg.Prompt)

	var (
		line  []byte
		count int
	)
	buf := make([]byte, 512)
	for {
		n, err := ch.Read(buf)
		for _, b := range buf[:n] {
			switch b {
			case '\r', '\n':
				if !s.cfg.NoEcho {
					io.WriteString(ch, "\r\n")
				}
				cmd := strings.TrimSpace(string(line))
				line = line[:0]
				count++
				// The drop happens when the command AFTER the last one
				// allowed arrives, unanswered -- not straight after
				// answering the last one. Closing right behind a write
				// races the peer reading it: a close with unread data in
				// the receive buffer sends RST, and a RST can discard a
				// prompt the client has been sent but not yet read, so the
				// drop meant for the next command landed during this one
				// (1 run in 8 under -race). On arrival the race is gone:
				// the client sends this line only after reading the last
				// prompt.
				if s.cfg.DropAfter > 0 && count > s.cfg.DropAfter {
					conn.Close()
					return
				}
				if !s.answer(ch, cmd) {
					// Wedged. Keep the channel open and keep draining
					// input, because that is the whole difference
					// between a stuck command and a dropped link — and
					// closing here would make this fixture produce the
					// wrong one.
					s.drain(ch)
					return
				}
			case 0x7f, 0x08: // DEL / BS
				if len(line) > 0 {
					line = line[:len(line)-1]
					if !s.cfg.NoEcho {
						io.WriteString(ch, "\b \b")
					}
				}
			default:
				line = append(line, b)
				if !s.cfg.NoEcho {
					ch.Write([]byte{b})
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// answer writes one command's response. It reports false when the shell
// should stop driving the loop — currently only a hang, where the point is
// that no prompt ever follows.
func (s *Server) answer(ch ssh.Channel, cmd string) bool {
	if cmd == "" {
		io.WriteString(ch, s.cfg.Prompt)
		return true
	}

	s.mu.Lock()
	s.asked = append(s.asked, cmd)
	s.mu.Unlock()

	if s.cfg.Latency > 0 {
		time.Sleep(s.cfg.Latency)
	}

	for _, h := range s.cfg.Hang {
		if h == cmd {
			// Output, then silence. The channel stays open, which is
			// what makes this a wedged command rather than a drop.
			s.write(ch, "gathering data...")
			return false
		}
	}

	if nbytes, ok := s.cfg.Flood[cmd]; ok {
		s.flood(ch, nbytes)
		io.WriteString(ch, s.cfg.Prompt)
		return true
	}

	out, ok := s.cfg.Commands[cmd]
	if !ok {
		out = s.cfg.Unknown
	}
	if out != "" {
		s.write(ch, out)
	}
	io.WriteString(ch, s.cfg.Prompt)
	return true
}

// write emits text CRLF-terminated, optionally in chunks.
func (s *Server) write(ch ssh.Channel, text string) {
	body := strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\r\n") + "\r\n"
	if s.cfg.ChunkSize <= 0 {
		io.WriteString(ch, body)
		return
	}
	for i := 0; i < len(body); i += s.cfg.ChunkSize {
		end := i + s.cfg.ChunkSize
		if end > len(body) {
			end = len(body)
		}
		io.WriteString(ch, body[i:end])
		if s.cfg.ChunkDelay > 0 {
			time.Sleep(s.cfg.ChunkDelay)
		}
	}
}

// drain reads and discards until the peer goes away. A wedged device is
// still a listening device: it accepts what you type, it just never
// answers.
func (s *Server) drain(ch ssh.Channel) {
	buf := make([]byte, 512)
	for {
		if _, err := ch.Read(buf); err != nil {
			return
		}
	}
}

// FloodLine is the line flood repeats, exported so a test can compute the
// exact line count it expects rather than guessing at a byte total. Byte
// totals do not survive the trip: the device sends CRLF, netexec
// normalizes it to LF, and the difference is one byte per line.
const FloodLine = "interface GigabitEthernet0/0/0/0 description lab fill line"

// FloodLines reports how many lines a Flood entry of n bytes produces.
func FloodLines(n int) int {
	per := len(FloodLine) + 2 // CRLF on the wire
	lines := n / per
	if n%per != 0 {
		lines++
	}
	return lines
}

// flood emits roughly n bytes of plausible config-shaped output. The text
// is line-oriented on purpose: a reader that scans for a prompt does more
// work per line than per byte, and a flood made of one enormous line would
// understate that.
func (s *Server) flood(ch ssh.Channel, n int) {
	line := FloodLine + "\r\n"
	for i := 0; i < FloodLines(n); i++ {
		io.WriteString(ch, line)
	}
}
