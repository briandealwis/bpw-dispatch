// Package retry retries transient network failures with exponential
// backoff, bounded by an overall time budget so a flaky server can't stretch
// a single run indefinitely.
package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/logging"
)

// Policy controls how Do retries.
type Policy struct {
	// MaxRetries is the number of retries after the first attempt.
	MaxRetries int
	// InitialBackoff is the wait before the first retry; it doubles for each
	// subsequent retry, capped at MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// Budget caps the total time spent across all attempts and waits. Zero
	// means no cap beyond the caller's context.
	Budget time.Duration
	// Sleep waits for d or until ctx is done; nil means a real timer. Tests
	// substitute one that doesn't actually wait.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Default retries up to 5 times (1s, 2s, 4s, 8s, 16s apart) but gives up
// once a minute has been spent, since each attempt may itself take up to
// the client's 15s request timeout.
var Default = Policy{
	MaxRetries:     5,
	InitialBackoff: time.Second,
	MaxBackoff:     16 * time.Second,
	Budget:         time.Minute,
}

// Do calls op until it succeeds, returns a non-transient error, the retries
// are used up, or the budget runs out; it returns op's last error. The
// context passed to op carries the budget deadline.
func Do(ctx context.Context, p Policy, log logging.Logger, name string, op func(context.Context) error) error {
	if p.Budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Budget)
		defer cancel()
	}
	sleep := p.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}

	backoff := p.InitialBackoff
	for attempt := 1; ; attempt++ {
		err := op(ctx)
		if err == nil {
			if attempt > 1 {
				logging.Logf(log, "%s: succeeded on attempt %d", name, attempt)
			}
			return nil
		}
		if attempt > p.MaxRetries || !IsTransient(err) {
			return err
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < backoff {
			logging.Logf(log, "%s: attempt %d failed (%v); not enough budget left to retry", name, attempt, err)
			return err
		}
		logging.Logf(log, "%s: attempt %d failed (%v); retrying in %s", name, attempt, err, backoff)
		if sleep(ctx, backoff) != nil {
			return err
		}
		backoff *= 2
		if p.MaxBackoff > 0 && backoff > p.MaxBackoff {
			backoff = p.MaxBackoff
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StatusError is returned by this project's HTTP clients when a server
// responds with an error status, so callers (and IsTransient) can tell a
// server-side failure apart from a network one.
type StatusError struct {
	Op         string
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s returned HTTP %d", e.Op, e.StatusCode)
	}
	return fmt.Sprintf("%s returned HTTP %d: %s", e.Op, e.StatusCode, e.Body)
}

// IsTransient reports whether err looks like a failure that might succeed
// on retry: timeouts, DNS failures, connection-level errors, and 5xx/429
// responses. Anything else (a 4xx, a login rejection, a page that didn't
// parse) is treated as permanent — retrying won't change the answer.
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.StatusCode >= 500 || se.StatusCode == 429
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// Even "no such host" is retried: a misbehaving resolver reports
		// that for names that do exist, which is exactly the failure that
		// prompted this retry logic.
		return true
	}
	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED)
}
