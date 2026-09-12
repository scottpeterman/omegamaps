// capi/notify_windows.go
//go:build windows && cgo

// Wakeup primitive for Windows. There are no pipes QSocketNotifier can
// watch, so the equivalent is a connected loopback socket pair: the read end
// is a SOCKET, which QSocketNotifier accepts directly on Windows.
//
// The sockets are created through raw winsock calls rather than net.Dial on
// purpose. A net.Conn is owned by the Go netpoller, and handing its
// underlying SOCKET to C++ while the poller also drives it invites exactly
// the kind of intermittent, unreproducible failure that costs a weekend.
// Nothing here is ever wrapped in a net.Conn.
//
// accept, ioctlsocket and send are called through ws2_32 directly:
// x/sys/windows either omits them or stubs them out with EWINDOWS, since the
// Go runtime normally reaches sockets through overlapped I/O instead.
package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ws2_32          = windows.NewLazySystemDLL("ws2_32.dll")
	procAccept      = ws2_32.NewProc("accept")
	procIoctlsocket = ws2_32.NewProc("ioctlsocket")
	procSend        = ws2_32.NewProc("send")
)

// fionbio is the ioctlsocket command that toggles non-blocking mode.
const fionbio = 0x8004667E

const invalidSocket = ^uintptr(0)

func sockAccept(listener windows.Handle) (windows.Handle, error) {
	r, _, err := procAccept.Call(uintptr(listener), 0, 0)
	if r == invalidSocket {
		return windows.InvalidHandle, err
	}
	return windows.Handle(r), nil
}

func sockSetNonblocking(s windows.Handle) error {
	mode := uint32(1)
	r, _, err := procIoctlsocket.Call(uintptr(s), uintptr(uint32(fionbio)),
		uintptr(unsafe.Pointer(&mode)))
	if int32(r) != 0 {
		return err
	}
	return nil
}

func sockSend(s windows.Handle, b []byte) {
	if len(b) == 0 {
		return
	}
	procSend.Call(uintptr(s), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0)
}

type notifier struct {
	r windows.Handle // read end, non-blocking; the caller watches this
	w windows.Handle // write end, poked by wake
}

func newNotifier() (*notifier, error) {
	listener, err := windows.Socket(windows.AF_INET, windows.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("notify socket: %w", err)
	}
	defer windows.Closesocket(listener)

	// Port 0 lets the stack pick; loopback only, so nothing is reachable
	// off-box even for the moment the listener is up.
	if err := windows.Bind(listener, &windows.SockaddrInet4{
		Port: 0,
		Addr: [4]byte{127, 0, 0, 1},
	}); err != nil {
		return nil, fmt.Errorf("notify bind: %w", err)
	}
	if err := windows.Listen(listener, 1); err != nil {
		return nil, fmt.Errorf("notify listen: %w", err)
	}
	sa, err := windows.Getsockname(listener)
	if err != nil {
		return nil, fmt.Errorf("notify getsockname: %w", err)
	}
	addr, ok := sa.(*windows.SockaddrInet4)
	if !ok {
		return nil, fmt.Errorf("notify getsockname: unexpected address family")
	}

	client, err := windows.Socket(windows.AF_INET, windows.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("notify client socket: %w", err)
	}
	if err := windows.Connect(client, &windows.SockaddrInet4{
		Port: addr.Port,
		Addr: [4]byte{127, 0, 0, 1},
	}); err != nil {
		windows.Closesocket(client)
		return nil, fmt.Errorf("notify connect: %w", err)
	}

	server, err := sockAccept(listener)
	if err != nil {
		windows.Closesocket(client)
		return nil, fmt.Errorf("notify accept: %w", err)
	}

	// Read end non-blocking so the caller's drain loop terminates on empty.
	if err := sockSetNonblocking(client); err != nil {
		windows.Closesocket(client)
		windows.Closesocket(server)
		return nil, fmt.Errorf("notify nonblock: %w", err)
	}
	// Wakes are one byte; Nagle would add latency for no benefit.
	if err := windows.SetsockoptInt(server, windows.IPPROTO_TCP,
		windows.TCP_NODELAY, 1); err != nil {
		windows.Closesocket(client)
		windows.Closesocket(server)
		return nil, fmt.Errorf("notify nodelay: %w", err)
	}

	return &notifier{r: client, w: server}, nil
}

// handle is the SOCKET the caller watches. QSocketNotifier takes it as-is.
func (n *notifier) handle() int { return int(n.r) }

// wake sends one byte. A failed send means the peer is gone or the buffer is
// full, and in both cases there is nothing useful to do about it here: a full
// buffer already holds an unread wakeup, and one wakeup is as good as ten.
func (n *notifier) wake() {
	if n.w == windows.InvalidHandle {
		return
	}
	sockSend(n.w, []byte{1})
}

func (n *notifier) close() {
	if n.w != windows.InvalidHandle {
		windows.Closesocket(n.w)
		n.w = windows.InvalidHandle
	}
	if n.r != windows.InvalidHandle {
		windows.Closesocket(n.r)
		n.r = windows.InvalidHandle
	}
}
