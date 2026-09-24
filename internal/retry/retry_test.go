package retry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"
)

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }

// recordingSleep returns a Sleep func that records requested waits without
// actually waiting.
func recordingSleep(waits *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return nil
	}
}

func TestDo_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Default, nil, "op", func(context.Context) error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("err=%v calls=%d, want nil and 1", err, calls)
	}
}

func TestDo_RetriesTransientWithExponentialBackoff(t *testing.T) {
	var waits []time.Duration
	p := Default
	p.Sleep = recordingSleep(&waits)

	calls := 0
	err := Do(context.Background(), p, nil, "op", func(context.Context) error {
		calls++
		if calls < 4 {
			return timeoutErr{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 4 {
		t.Errorf("calls = %d, want 4", calls)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if fmt.Sprint(waits) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v", waits, want)
	}
}

func TestDo_GivesUpAfterMaxRetries(t *testing.T) {
	var waits []time.Duration
	p := Default
	p.Sleep = recordingSleep(&waits)

	calls := 0
	err := Do(context.Background(), p, nil, "op", func(context.Context) error {
		calls++
		return timeoutErr{}
	})
	if err == nil {
		t.Fatal("expected the last error once retries are exhausted")
	}
	if calls != 6 {
		t.Errorf("calls = %d, want 6 (1 attempt + 5 retries)", calls)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	if fmt.Sprint(waits) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v", waits, want)
	}
}

func TestDo_DoesNotRetryPermanentErrors(t *testing.T) {
	calls := 0
	permanent := errors.New("login rejected")
	err := Do(context.Background(), Default, nil, "op", func(context.Context) error {
		calls++
		return permanent
	})
	if !errors.Is(err, permanent) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want the permanent error after 1 call", err, calls)
	}
}

func TestDo_StopsWhenBudgetCannotCoverNextBackoff(t *testing.T) {
	p := Policy{MaxRetries: 5, InitialBackoff: time.Second, Budget: 50 * time.Millisecond}
	calls := 0
	start := time.Now()
	err := Do(context.Background(), p, nil, "op", func(context.Context) error {
		calls++
		return timeoutErr{}
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1: a 1s backoff can't fit in a 50ms budget", calls)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s; should give up immediately rather than wait out the backoff", elapsed)
	}
}

func TestDo_OpSeesBudgetDeadline(t *testing.T) {
	p := Policy{Budget: time.Minute}
	err := Do(context.Background(), p, nil, "op", func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("no deadline on op's context")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIsTransient(t *testing.T) {
	wrap := func(err error) error { return &url.Error{Op: "Get", URL: "https://example.com", Err: err} }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("could not parse page"), false},
		{"context canceled", context.Canceled, false},
		{"timeout", wrap(timeoutErr{}), true},
		{"context deadline", context.DeadlineExceeded, true},
		{"DNS not found", wrap(&net.DNSError{Name: "x", IsNotFound: true}), true},
		{"DNS timeout", wrap(&net.DNSError{Name: "x", IsTimeout: true}), true},
		{"connection refused", wrap(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), true},
		{"connection reset", wrap(syscall.ECONNRESET), true},
		{"HTTP 503", &StatusError{Op: "x", StatusCode: 503}, true},
		{"HTTP 429", &StatusError{Op: "x", StatusCode: 429}, true},
		{"HTTP 404", &StatusError{Op: "x", StatusCode: 404}, false},
		{"HTTP 403", fmt.Errorf("wrapped: %w", &StatusError{Op: "x", StatusCode: 403}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTransient(c.err); got != c.want {
				t.Errorf("IsTransient(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
