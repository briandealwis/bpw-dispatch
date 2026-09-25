package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/retry"
	"github.com/briandealwis/bpw-dispatch/internal/session"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

func TestFormatStatusMessage_NoAlerts(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	leg := &state.Leg{Bus: "140"}
	got := formatStatusMessage(kid, session.Morning, leg, nil)
	want := "Example School, 140: Operating as scheduled"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_MatchesUserExample(t *testing.T) {
	// The exact wording style requested: school + bus, no child's name.
	kid := config.Kid{School: "École élémentaire L'Odyssée", BusLabel: "Route 140"}
	alerts := []alertsapi.Alert{{Action: "Bus delayed by 10-15 minutes"}}
	got := formatStatusMessage(kid, session.Morning, nil, alerts)
	want := "École élémentaire L'Odyssée, Route 140: Bus delayed by 10-15 minutes"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_MultipleAlertsJoined(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	alerts := []alertsapi.Alert{
		{Action: "Bus Delayed - 10 to 19 minutes"},
		{Action: "Stop Relocated"},
	}
	got := formatStatusMessage(kid, session.Morning, nil, alerts)
	want := "Example School, 140: Bus Delayed - 10 to 19 minutes; Stop Relocated"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_IncludesScheduledTimeForSession(t *testing.T) {
	kid := config.Kid{School: "École élémentaire L'Odyssée", BusLabel: "Route 140"}
	morning := &state.Leg{Bus: "140", PickupTime: "7:58 AM", DropoffTime: "8:45 AM"}
	afternoon := &state.Leg{Bus: "140", PickupTime: "3:30 PM", DropoffTime: "3:53 PM"}
	delayed := []alertsapi.Alert{{Action: "Bus delayed by 10-15 minutes"}}

	cases := []struct {
		name   string
		sess   session.Session
		leg    *state.Leg
		alerts []alertsapi.Alert
		want   string
	}{
		{"morning shows home pickup", session.Morning, morning, nil,
			"École élémentaire L'Odyssée, Route 140: Operating as scheduled (pickup 7:58 AM)"},
		{"afternoon shows drop-off home", session.Afternoon, afternoon, nil,
			"École élémentaire L'Odyssée, Route 140: Operating as scheduled (drop-off 3:53 PM)"},
		{"delay keeps the scheduled time for reference", session.Morning, morning, delayed,
			"École élémentaire L'Odyssée, Route 140: Bus delayed by 10-15 minutes (pickup 7:58 AM)"},
		{"unknown schedule omits the time", session.Morning, nil, nil,
			"École élémentaire L'Odyssée, Route 140: Operating as scheduled"},
		{"missing time omits it", session.Afternoon, &state.Leg{Bus: "140"}, nil,
			"École élémentaire L'Odyssée, Route 140: Operating as scheduled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatStatusMessage(kid, c.sess, c.leg, c.alerts); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestFormatAlertsError(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	got := formatAlertsError(kid, nil, errors.New("boom"))
	want := "Example School, 140: Unable to check bus status (boom)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAlertsError_Timeout(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()
	got := formatAlertsError(kid, nil, ctx.Err())
	want := "Example School, 140: Unable to check bus status (timed out)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAlertsStale(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	since := time.Date(2026, 9, 24, 7, 5, 0, 0, time.UTC)
	got := formatAlertsStale(kid, nil, since, &retry.StatusError{Op: "GetBusNotifications", StatusCode: 503})
	want := "Example School, 140: Unable to check bus status since 07:05 (server returned HTTP 503)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduleError(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	got := formatScheduleError(kid, errors.New("portal unreachable"))
	want := "Example School: Unable to refresh today's schedule (portal unreachable)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduleStale(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	since := time.Date(2026, 9, 24, 6, 30, 0, 0, time.UTC)
	got := formatScheduleStale(kid, since, errors.New("portal unreachable"))
	want := "Example School: Unable to refresh today's schedule since 06:30 (portal unreachable)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_StatusError(t *testing.T) {
	err := fmt.Errorf("fetching login page: %w", &retry.StatusError{Op: "portal GET /Login", StatusCode: 502, Body: "<html>long error page</html>"})
	want := "server returned HTTP 502"
	if got := describeErr(err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_Timeout(t *testing.T) {
	timeoutErr := &timeoutError{}
	if got := describeErr(timeoutErr); got != "timed out" {
		t.Errorf("got %q, want %q", got, "timed out")
	}
}

func TestDescribeErr_NonTimeout(t *testing.T) {
	if got := describeErr(errors.New("some other failure")); got != "some other failure" {
		t.Errorf("got %q, want %q", got, "some other failure")
	}
}

// wrapAsClientErr mimics how net/http actually surfaces a low-level network
// error from Client.Do: wrapped in a *url.Error carrying the request URL.
// describeErr must unwrap through this the same way it would for a real
// failed request, and must not let that URL leak into the returned text.
func wrapAsClientErr(err error) error {
	return &url.Error{Op: "Get", URL: "https://parent-with-secret-token.example.com/Login", Err: err}
}

func TestDescribeErr_DNSTimeout(t *testing.T) {
	err := wrapAsClientErr(&net.DNSError{Err: "i/o timeout", Name: "www.findmyschool.ca", IsTimeout: true})
	want := "DNS lookup for www.findmyschool.ca timed out"
	if got := describeErr(err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_DNSNotFound(t *testing.T) {
	err := wrapAsClientErr(&net.DNSError{Err: "no such host", Name: "typo.example.com", IsNotFound: true})
	want := "DNS lookup for typo.example.com failed (no such host)"
	if got := describeErr(err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_DNSOtherFailure(t *testing.T) {
	err := wrapAsClientErr(&net.DNSError{Err: "server misbehaving", Name: "www.findmyschool.ca"})
	want := "DNS lookup for www.findmyschool.ca failed (server misbehaving)"
	if got := describeErr(err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_DNSTakesPrecedenceOverGenericTimeout(t *testing.T) {
	// net.DNSError also implements Timeout() bool, so if the generic timeout
	// check ran first this would come back as the less useful "timed out"
	// instead of naming the DNS lookup.
	err := wrapAsClientErr(&net.DNSError{Err: "i/o timeout", Name: "infobus.francobus.ca", IsTimeout: true})
	if got := describeErr(err); got == "timed out" {
		t.Errorf("DNS-specific detail was lost to the generic timeout branch: %q", got)
	}
}

func TestDescribeErr_ConnectionRefused(t *testing.T) {
	err := wrapAsClientErr(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED})
	want := "connection refused"
	if got := describeErr(err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_TLSUntrustedCert(t *testing.T) {
	err := wrapAsClientErr(x509.UnknownAuthorityError{})
	want := "TLS handshake failed"
	if got := describeErr(err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_TLSRecordHeader(t *testing.T) {
	err := wrapAsClientErr(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"})
	want := "TLS handshake failed"
	if got := describeErr(err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeErr_NeverLeaksRequestURL(t *testing.T) {
	err := wrapAsClientErr(&net.DNSError{Err: "i/o timeout", Name: "example.com", IsTimeout: true})
	if got := describeErr(err); got == err.Error() {
		t.Errorf("describeErr should not fall back to the raw (URL-containing) error text here: %q", got)
	}
}

// timeoutError implements the net.Error-style Timeout() bool interface that
// describeErr checks for, without needing a real network round-trip.
type timeoutError struct{}

func (*timeoutError) Error() string   { return "i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }

func TestBusLabel_FallbackOrder(t *testing.T) {
	leg := &state.Leg{Bus: "scraped-bus"}
	cases := []struct {
		name string
		kid  config.Kid
		leg  *state.Leg
		want string
	}{
		{"explicit label wins", config.Kid{BusLabel: "Route 140", AlertMatch: config.AlertMatch{Bus: "other"}}, leg, "Route 140"},
		{"falls back to scraped bus", config.Kid{}, leg, "scraped-bus"},
		{"falls back to configured alert_match.bus", config.Kid{AlertMatch: config.AlertMatch{Bus: "configured-bus"}}, nil, "configured-bus"},
		{"falls back to generic label", config.Kid{}, nil, "bus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := busLabel(c.kid, c.leg); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestFormatScheduleChange_BusChanged(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	old := &state.Schedule{Morning: &state.Leg{Bus: "140", PickupTime: "7:58 AM", PickupLocation: "Elm St"}}
	updated := &state.Schedule{Morning: &state.Leg{Bus: "141", PickupTime: "7:58 AM", PickupLocation: "Elm St"}}
	got := formatScheduleChange(kid, old, updated)
	want := "Example School: schedule changed - morning: bus now 141 (was 140)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduleChange_LegAdded(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	old := &state.Schedule{Morning: &state.Leg{Bus: "140"}, Afternoon: nil}
	updated := &state.Schedule{
		Morning:   &state.Leg{Bus: "140"},
		Afternoon: &state.Leg{Bus: "140", PickupTime: "3:30 PM", PickupLocation: "School", DropoffTime: "3:53 PM", DropoffLocation: "Elm St"},
	}
	got := formatScheduleChange(kid, old, updated)
	want := "Example School: schedule changed - afternoon bus added: 140, pickup 3:30 PM at School, dropoff 3:53 PM at Elm St"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduleChange_LegRemoved(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	old := &state.Schedule{Morning: &state.Leg{Bus: "140"}, Afternoon: &state.Leg{Bus: "140"}}
	updated := &state.Schedule{Morning: &state.Leg{Bus: "140"}, Afternoon: nil}
	got := formatScheduleChange(kid, old, updated)
	want := "Example School: schedule changed - afternoon bus removed (was 140)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
