package app

import (
	"fmt"
	"strings"

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

// formatAlertMessage builds the per-session alert text. It deliberately
// omits the kid's name — only school and bus identify the message.
func formatAlertMessage(kid config.Kid, leg *state.Leg, alerts []alertsapi.Alert) string {
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
