// capi/notify_darwin.go
//go:build darwin && cgo

// Wakeup primitive for macOS. Same self-pipe as the Linux file, created
// differently: there is no pipe2(2) on Darwin, so syscall.Pipe2 does not
// exist and close-on-exec has to be set as a second step.
//
// os.Pipe is deliberately NOT used, for the same reason as on Linux: the Go
// netpoller adopts an os.File read end and File.Fd() then hands back a
// descriptor forced into blocking mode, which deadlocks a C++ drain loop on
// an empty pipe.
package main

import "syscall"

type notifier struct {
	r int
	w int
}

func newNotifier() (*notifier, error) {
	var fds [2]int

	// ForkLock held across pipe + CloseOnExec because those are two calls
	// here, not one. Between them a fork in any other goroutine would
	// inherit both descriptors. This is exactly what os.Pipe does on the
	// platforms without pipe2, and the reason the Linux file can skip it.
	syscall.ForkLock.RLock()
	if err := syscall.Pipe(fds[:]); err != nil {
		syscall.ForkLock.RUnlock()
		return nil, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	syscall.ForkLock.RUnlock()

	// Both ends non-blocking: the reader must not stall on an empty pipe,
	// and the writer must not stall on a full one.
	if err := syscall.SetNonblock(fds[0], true); err != nil {
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		return nil, err
	}
	if err := syscall.SetNonblock(fds[1], true); err != nil {
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		return nil, err
	}
	return &notifier{r: fds[0], w: fds[1]}, nil
}

// handle is the descriptor the caller watches.
func (n *notifier) handle() int { return n.r }

// wake pokes the pipe. A full pipe means an unread wakeup is already
// pending, so the dropped write costs nothing — one wakeup is as good as ten.
func (n *notifier) wake() {
	if n.w < 0 {
		return
	}
	syscall.Write(n.w, []byte{1})
}

func (n *notifier) close() {
	if n.w >= 0 {
		syscall.Close(n.w)
		n.w = -1
	}
	if n.r >= 0 {
		syscall.Close(n.r)
		n.r = -1
	}
}
