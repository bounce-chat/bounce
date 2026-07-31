//go:build android

package goengine

/*
#cgo LDFLAGS: -llog
#include <android/log.h>
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"unsafe"

	log "github.com/sirupsen/logrus"
)

// gomobile bind, unlike gomobile build, does not link x/mobile's mobileinit,
// so nothing redirects the Go side's stderr into logcat. logrus therefore
// writes the entire engine log - Tor bootstrap, peering, frame handling - to a
// file descriptor nothing on Android is reading, and `adb logcat` shows only
// whatever Kotlin logged.
//
// mobileinit is an internal package of x/mobile and cannot be imported from
// here, so this does the same job directly: a logrus sink that calls
// __android_log_write.
//
// The engine also keeps its own rotating copy at
// <configDir>/bounce-log.txt via the file hook in chat/log.go; this is purely
// so the log is visible live.
type androidLogWriter struct {
	tag *C.char
}

func (w *androidLogWriter) Write(p []byte) (int, error) {
	// logrus hands over one formatted record per Write, with a trailing
	// newline that would otherwise show as a blank line in logcat.
	msg := C.CString(string(bytes.TrimRight(p, "\n")))
	defer C.free(unsafe.Pointer(msg))

	C.__android_log_write(C.ANDROID_LOG_INFO, w.tag, msg)
	return len(p), nil
}

var androidTag = C.CString("BounceGo")

func installPlatformLogger() {
	log.SetOutput(&androidLogWriter{tag: androidTag})
	// Colour escapes are noise in logcat, and logrus cannot detect that this
	// writer is not a terminal.
	log.SetFormatter(&log.TextFormatter{DisableColors: true, FullTimestamp: true})
}
