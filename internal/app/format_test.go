package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"syscall"
	"testing"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

func TestFormatStatusMessage_NoAlerts(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	leg := &state.Leg{Bus: "140"}
	got := formatStatusMessage(kid, leg, nil, nil, nil)
	want := "Example School, 140: Operating as scheduled"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_MatchesUserExample(t *testing.T) {
	// The exact wording style requested: school + bus, no child's name.
	kid := config.Kid{School: "École élémentaire L'Odyssée", BusLabel: "Route 140"}
	alerts := []alertsapi.Alert{{Action: "Bus delayed by 10-15 minutes"}}
	got := formatStatusMessage(kid, nil, alerts, nil, nil)
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
	got := formatStatusMessage(kid, nil, alerts, nil, nil)
	want := "Example School, 140: Bus Delayed - 10 to 19 minutes; Stop Relocated"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_AlertsErrorReportedInsteadOfSilentlySkipped(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	got := formatStatusMessage(kid, nil, nil, errors.New("boom"), nil)
	want := "Example School, 140: Unable to check bus status (boom)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_AlertsErrorTimeout(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()
	got := formatStatusMessage(kid, nil, nil, ctx.Err(), nil)
	want := "Example School, 140: Unable to check bus status (timed out)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_ScheduleErrorAppendedAsNote(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	got := formatStatusMessage(kid, nil, nil, nil, errors.New("portal unreachable"))
	want := "Example School, 140: Operating as scheduled [could not refresh today's schedule: portal unreachable]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatStatusMessage_BothErrors(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	got := formatStatusMessage(kid, nil, nil, errors.New("alerts down"), errors.New("portal down"))
	want := "Example School, 140: Unable to check bus status (alerts down) [could not refresh today's schedule: portal down]"
	if got != want {
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
