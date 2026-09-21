package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogf_NilLoggerIsSafe(t *testing.T) {
	// Must not panic: callers rely on being able to log through a Logger
	// field that a fake/test double never set.
	var l Logger
	Logf(l, "hello %s", "world")
	Logf(nil, "hello %s", "world")
	Logf(Discard, "hello %s", "world")
}

func TestNew_WritesFormattedLine(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	Logf(l, "portal %s: GET %s starting", "example.com", "/Login")

	out := buf.String()
	if !strings.Contains(out, "portal example.com: GET /Login starting") {
		t.Errorf("output = %q, missing expected message", out)
	}
	if !strings.Contains(out, ":") {
		t.Errorf("output = %q, expected a timestamp prefix", out)
	}
}

func TestDiscard_WritesNothing(t *testing.T) {
	// Discard has no backing writer to inspect, so this just documents
	// (and exercises) that calling it is a safe no-op.
	Logf(Discard, "should not panic or block")
}
