// Package notify sends alert text to configured destinations (currently
// ntfy.sh; the Notifier interface leaves room for other backends later).
package notify

import (
	"context"
	"fmt"

	"github.com/briandealwis/bpw-dispatch/internal/config"
)

// Notifier delivers a single text message to one destination.
type Notifier interface {
	Send(ctx context.Context, text string) error
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
