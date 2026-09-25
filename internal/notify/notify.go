// Package notify sends alert text to configured destinations (currently
// ntfy.sh; the Notifier interface leaves room for other backends later).
package notify

import (
	"context"
	"fmt"

	"github.com/briandealwis/bpw-dispatch/internal/config"
)

// Message priorities, following ntfy's 1–5 scale: low arrives silently,
// default with a short vibration and sound, high with a long vibration and a
// pop-over, and urgent with very long vibration bursts. Zero leaves the
// priority to the notifier's default.
const (
	PriorityLow     = 2
	PriorityDefault = 3
	PriorityHigh    = 4
	PriorityUrgent  = 5
)

// Message is a single alert to deliver.
type Message struct {
	Text string
	// ClickURL, if set, is opened when the notification itself is tapped
	// (e.g. ntfy's "Click" action), so the recipient can jump straight to
	// the relevant portal page for details.
	ClickURL string
	// Priority is one of the Priority* constants, or zero for the default.
	Priority int
	// Tags label the message. ntfy shows tags that are emoji shortcodes
	// (e.g. "bus", "warning") as emoji in front of the message.
	Tags []string
}

// Notifier delivers a single message to one destination.
type Notifier interface {
	Send(ctx context.Context, msg Message) error
}

// Build constructs the configured notifier registry, keyed by name.
func Build(notifiers map[string]config.Notifier) (map[string]Notifier, error) {
	out := make(map[string]Notifier, len(notifiers))
	for name, cfg := range notifiers {
		n, err := build(cfg)
		if err != nil {
			return nil, fmt.Errorf("notifier %q: %w", name, err)
		}
		out[name] = n
	}
	return out, nil
}

func build(cfg config.Notifier) (Notifier, error) {
	switch cfg.Type {
	case "ntfy":
		return NewNtfy(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported notifier type %q (only \"ntfy\" is currently implemented)", cfg.Type)
	}
}
