// Package logging provides an optional verbose diagnostic logger, used to
// trace where a run is spending time (or stuck) — e.g. login/HTTP calls
// that never got a chance to time out because the process itself hung.
package logging

import (
	"io"
	"log"
)

// Logger writes a formatted diagnostic line. A nil Logger is valid to call
// through the package-level Logf helper below, which treats nil as Discard.
type Logger interface {
	Printf(format string, v ...any)
}

// Discard drops everything logged to it.
var Discard Logger = discard{}

type discard struct{}

func (discard) Printf(string, ...any) {}

// New returns a Logger that writes timestamped (to microsecond precision,
// so near-simultaneous "starting"/"finished" lines around a hung call are
// still orderable) lines to w.
func New(w io.Writer) Logger {
	return stdLogger{log.New(w, "", log.LstdFlags|log.Lmicroseconds)}
}

type stdLogger struct{ *log.Logger }

// Logf calls l.Printf if l is non-nil; it's a nil-safe way for callers to
// log without checking for a nil Logger field at every call site (a zero
// value Client/App in a test won't have one set).
func Logf(l Logger, format string, v ...any) {
	if l == nil {
		return
	}
	l.Printf(format, v...)
}
