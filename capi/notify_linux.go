// capi/notify_linux.go
//go:build linux && cgo

// Wakeup primitive for Linux: a self-pipe whose read end the caller hands to
// QSocketNotifier.
//
// os.Pipe is deliberately NOT used. The Go netpoller adopts an os.File read
// end, and File.Fd() then returns a descriptor forced back into blocking
// mode -- a C++ drain loop deadlocks on it the first time the pipe is empty.
// Raw descriptors from Pipe2 stay out of the netpoller entirely.
//
// Darwin has its own file: macOS never implemented the pipe2(2) syscall, so
// syscall.Pipe2 does not exist there. This file was originally named
// notify_posix.go and guarded !windows, which compiled on Linux and failed on
// the first Mac that saw it.
package main

import "syscall"

type notifier struct {
	r int
	w int
}

func newNotifier() (*notifier, error) {
	var fds [2]int
	// Pipe2 sets close-on-exec atomically. That matters: a fork between
	// creating the pipe and marking it would leak both ends into the child.
	if err := syscall.Pipe2(fds[:], syscall.O_CLOEXEC); err != nil {
		return nil, err
	}
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
