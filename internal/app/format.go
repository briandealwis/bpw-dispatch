package app

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/retry"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

// busLabel picks the text used for the bus in alert messages: the kid's
// configured display label, else the scraped/matched bus identifier, else
// a generic fallback.
func busLabel(kid config.Kid, leg *state.Leg) string {
	if kid.BusLabel != "" {
		return kid.BusLabel
	}
	if leg != nil && leg.Bus != "" {
		return leg.Bus
	}
	if kid.AlertMatch.Bus != "" {
		return kid.AlertMatch.Bus
	}
	return "bus"
}

// formatStatusMessage builds the per-session status text sent to
// notifiers after a successful alerts check. It deliberately omits the
// kid's name — only school and bus identify the message.
func formatStatusMessage(kid config.Kid, leg *state.Leg, alerts []alertsapi.Alert) string {
	status := "Operating as scheduled"
	if len(alerts) > 0 {
		parts := make([]string, 0, len(alerts))
		for _, al := range alerts {
			parts = append(parts, al.Action)
		}
		status = strings.Join(parts, "; ")
	}
	return fmt.Sprintf("%s, %s: %s", kid.School, busLabel(kid, leg), status)
}

// formatAlertsError is sent to the error notifiers when an alerts check
// fails.
func formatAlertsError(kid config.Kid, leg *state.Leg, err error) string {
	return fmt.Sprintf("%s, %s: Unable to check bus status (%s)", kid.School, busLabel(kid, leg), describeErr(err))
}

// formatAlertsStale is sent to a session's regular notifiers once the
// alerts check has been failing for longer than stale_after.
func formatAlertsStale(kid config.Kid, leg *state.Leg, since time.Time, err error) string {
	return fmt.Sprintf("%s, %s: Unable to check bus status since %s (%s)",
		kid.School, busLabel(kid, leg), since.Format("15:04"), describeErr(err))
}

// formatAlertsRecovered is sent to the error notifiers when an alerts check
// succeeds after failing.
func formatAlertsRecovered(kid config.Kid, leg *state.Leg) string {
	return fmt.Sprintf("%s, %s: Bus status check recovered", kid.School, busLabel(kid, leg))
}

// formatScheduleError is sent to the error notifiers when today's schedule
// refresh fails.
func formatScheduleError(kid config.Kid, err error) string {
	return fmt.Sprintf("%s: Unable to refresh today's schedule (%s)", kid.School, describeErr(err))
}

// formatScheduleStale is sent to a kid's regular notifiers once today's
// schedule refresh has been failing for longer than stale_after.
func formatScheduleStale(kid config.Kid, since time.Time, err error) string {
	return fmt.Sprintf("%s: Unable to refresh today's schedule since %s (%s)",
		kid.School, since.Format("15:04"), describeErr(err))
}

// formatScheduleRecovered is sent to the error notifiers (and, if they were
// told about the failure, the regular notifiers) when the schedule refresh
// succeeds after failing.
func formatScheduleRecovered(kid config.Kid) string {
	return fmt.Sprintf("%s: Schedule refresh recovered", kid.School)
}

// describeErr classifies a network error into a short, readable phrase
// rather than the raw (often URL-containing) Go error text — clearer in a
// phone notification, doesn't leak request URLs, and names the likely
// cause (DNS, connection refused, TLS, or a generic timeout) so a repeat
// failure's category is visible without needing -verbose.
//
// Checks are ordered most- to least-specific: a DNS failure is also a
// net.Error whose Timeout() may be true, so it's matched first or it'd be
// reported as a bare "timed out" instead of naming the DNS lookup.
func describeErr(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		switch {
		case dnsErr.IsTimeout:
			return fmt.Sprintf("DNS lookup for %s timed out", dnsErr.Name)
		case dnsErr.IsNotFound:
			return fmt.Sprintf("DNS lookup for %s failed (no such host)", dnsErr.Name)
		default:
			return fmt.Sprintf("DNS lookup for %s failed (%s)", dnsErr.Name, dnsErr.Err)
		}
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused"
	}

	var statusErr *retry.StatusError
	if errors.As(err, &statusErr) {
		return fmt.Sprintf("server returned HTTP %d", statusErr.StatusCode)
	}

	var certErr x509.UnknownAuthorityError
	var tlsErr tls.RecordHeaderError
	if errors.As(err, &certErr) || errors.As(err, &tlsErr) {
		return "TLS handshake failed"
	}

	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		return "timed out"
	}
	return err.Error()
}

// formatScheduleChange builds the text sent when a kid's scraped schedule
// differs from the previous day's, describing what changed on each leg.
func formatScheduleChange(kid config.Kid, old, new *state.Schedule) string {
	var changes []string
	changes = append(changes, legChanges("morning", old.Morning, new.Morning)...)
	changes = append(changes, legChanges("afternoon", old.Afternoon, new.Afternoon)...)
	return fmt.Sprintf("%s: schedule changed - %s", kid.School, strings.Join(changes, "; "))
}

func legChanges(label string, old, new *state.Leg) []string {
	if old.Equal(new) {
		return nil
	}
	if old == nil {
		return []string{fmt.Sprintf("%s bus added: %s, pickup %s at %s, dropoff %s at %s",
			label, new.Bus, new.PickupTime, new.PickupLocation, new.DropoffTime, new.DropoffLocation)}
	}
	if new == nil {
		return []string{fmt.Sprintf("%s bus removed (was %s)", label, old.Bus)}
	}
	var parts []string
	if old.Bus != new.Bus {
		parts = append(parts, fmt.Sprintf("bus now %s (was %s)", new.Bus, old.Bus))
	}
	if old.PickupTime != new.PickupTime || old.PickupLocation != new.PickupLocation {
		parts = append(parts, fmt.Sprintf("pickup now %s at %s", new.PickupTime, new.PickupLocation))
	}
	if old.DropoffTime != new.DropoffTime || old.DropoffLocation != new.DropoffLocation {
		parts = append(parts, fmt.Sprintf("dropoff now %s at %s", new.DropoffTime, new.DropoffLocation))
	}
	return []string{fmt.Sprintf("%s: %s", label, strings.Join(parts, ", "))}
}
