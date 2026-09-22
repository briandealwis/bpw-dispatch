package app

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
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
// notifiers. It deliberately omits the kid's name — only school and bus
// identify the message.
//
// alertsErr, when non-nil, means the Alerts API call itself failed (e.g.
// timed out) — rather than silently skipping the notification in that
// case, the failure is reported as the status so a broken check doesn't
// look identical to no news at all. scheduleErr, when non-nil, means
// today's schedule refresh failed this run; it's appended as a side note
// so it doesn't get lost even though the schedule check retries silently
// on its own every run until it succeeds.
func formatStatusMessage(kid config.Kid, leg *state.Leg, alerts []alertsapi.Alert, alertsErr, scheduleErr error) string {
	var status string
	switch {
	case alertsErr != nil:
		status = fmt.Sprintf("Unable to check bus status (%s)", describeErr(alertsErr))
	case len(alerts) > 0:
		parts := make([]string, 0, len(alerts))
		for _, al := range alerts {
			parts = append(parts, al.Action)
		}
		status = strings.Join(parts, "; ")
	default:
		status = "Operating as scheduled"
	}

	msg := fmt.Sprintf("%s, %s: %s", kid.School, busLabel(kid, leg), status)
	if scheduleErr != nil {
		msg += fmt.Sprintf(" [could not refresh today's schedule: %s]", describeErr(scheduleErr))
	}
	return msg
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
