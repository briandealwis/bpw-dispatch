package app

import (
	"testing"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

func TestFormatAlertMessage_NoAlerts(t *testing.T) {
	kid := config.Kid{School: "Example School"}
	leg := &state.Leg{Bus: "140"}
	got := formatAlertMessage(kid, leg, nil)
	want := "Example School, 140: Operating as scheduled"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAlertMessage_MatchesUserExample(t *testing.T) {
	// The exact wording style requested: school + bus, no child's name.
	kid := config.Kid{School: "École élémentaire L'Odyssée", BusLabel: "Route 140"}
	alerts := []alertsapi.Alert{{Action: "Bus delayed by 10-15 minutes"}}
	got := formatAlertMessage(kid, nil, alerts)
	want := "École élémentaire L'Odyssée, Route 140: Bus delayed by 10-15 minutes"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAlertMessage_MultipleAlertsJoined(t *testing.T) {
	kid := config.Kid{School: "Example School", BusLabel: "140"}
	alerts := []alertsapi.Alert{
		{Action: "Bus Delayed - 10 to 19 minutes"},
		{Action: "Stop Relocated"},
	}
	got := formatAlertMessage(kid, nil, alerts)
	want := "Example School, 140: Bus Delayed - 10 to 19 minutes; Stop Relocated"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

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
