// capi/lasterror.go
//go:build cgo

// Detail text for the last failing call, keyed by the OS thread that made it.
// Taken from omegassh's capi, where a single global string was tried first
// and failed: two Qt threads calling in overwrite each other's message, and
// the symptom is a plausible, wrong error in a dialog.
//
// Keyed on the C thread rather than a goroutine because that is the identity
// the caller has. cgo pins the calling thread for the duration of an exported
// call, so a C++ caller that checks a return code and then reads the text is
// guaranteed its own.
//
// The other half is clearing on success, so a call that worked never reads
// back an older failure.
package main

/*
#cgo !windows LDFLAGS: -lpthread
#include <stdint.h>
#ifdef _WIN32
#include <windows.h>
static unsigned long long omegamaps_thread_id(void) {
    return (unsigned long long)GetCurrentThreadId();
}
#else
#include <pthread.h>
static unsigned long long omegamaps_thread_id(void) {
    return (unsigned long long)(uintptr_t)pthread_self();
}
#endif
*/
import "C"

import (
	"fmt"
	"sync"
)

// errDetail maps thread id -> most recent message on that thread. Entries are
// small and bounded by the number of threads that have ever called in, which
// for a GUI is a handful.
var errDetail sync.Map

// setErr records why the current call failed. Both surfaces use it.
func setErr(format string, a ...interface{}) {
	errDetail.Store(uint64(C.omegamaps_thread_id()), fmt.Sprintf(format, a...))
}

// clearErr drops the message for this thread, so a later successful call does
// not leave an older failure readable.
func clearErr() {
	errDetail.Delete(uint64(C.omegamaps_thread_id()))
}

//export omegamaps_last_error
func omegamaps_last_error() *C.char {
	if v, ok := errDetail.Load(uint64(C.omegamaps_thread_id())); ok {
		return C.CString(v.(string))
	}
	return C.CString("")
}
