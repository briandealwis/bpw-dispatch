package app

import (
	"strings"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
)

// style is how a kind of message is presented: its notification priority
// and tags (ntfy renders tags that are emoji shortcodes as emoji in front of
// the message).
type style struct {
	priority int
	tags     []string
}

var (
	// Regular notifiers.
	styleAllClear       = style{notify.PriorityDefault, []string{"bus", "white_check_mark"}} // 🚌✅
	styleBusAlert       = style{notify.PriorityHigh, []string{"bus", "warning"}}             // 🚌⚠️
	styleCancellation   = style{notify.PriorityUrgent, []string{"bus", "rotating_light"}}    // 🚌🚨
	styleScheduleChange = style{notify.PriorityHigh, []string{"bus", "calendar"}}            // 🚌📆
	styleUnavailable    = style{notify.PriorityHigh, []string{"bus", "x"}}                   // 🚌❌
	styleRecovered      = style{notify.PriorityDefault, []string{"bus", "white_check_mark"}} // 🚌✅

	// Error notifiers: diagnostic, so delivered silently.
	styleCheckFailed    = style{notify.PriorityLow, []string{"warning"}}          // ⚠️
	styleCheckRecovered = style{notify.PriorityLow, []string{"white_check_mark"}} // ✅
)

// statusStyle picks the style for a session status message: an all-clear,
// a bus alert (delay etc.), or — if any alert is a cancellation — urgent.
func statusStyle(alerts []alertsapi.Alert) style {
	if len(alerts) == 0 {
		return styleAllClear
	}
	for _, a := range alerts {
		if isCancellation(a.Action) {
			return styleCancellation
		}
	}
	return styleBusAlert
}

// isCancellation reports whether an alert's action text describes a
// cancelled run, in English ("Cancelled", "Canceled", "Cancellation") or
// French ("Annulé", "Route Annulée", "Annulation").
func isCancellation(action string) bool {
	a := strings.ToLower(action)
	return strings.Contains(a, "cancel") || strings.Contains(a, "annul")
}

func (s style) message(text, clickURL string) notify.Message {
	return notify.Message{Text: text, ClickURL: clickURL, Priority: s.priority, Tags: s.tags}
}
