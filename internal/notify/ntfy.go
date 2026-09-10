package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/config"
)

// Ntfy sends messages via an ntfy.sh (or self-hosted ntfy) topic.
type Ntfy struct {
	Server     string
	Topic      string
	HTTPClient *http.Client
}

// NewNtfy builds an Ntfy notifier from config, defaulting Server to
// https://ntfy.sh when unset.
func NewNtfy(cfg config.Notifier) *Ntfy {
	server := cfg.Server
	if server == "" {
		server = "https://ntfy.sh"
	}
	return &Ntfy{
		Server:     server,
		Topic:      cfg.Topic,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Send publishes text to the configured ntfy topic.
func (n *Ntfy) Send(ctx context.Context, text string) error {
	url := strings.TrimRight(n.Server, "/") + "/" + n.Topic
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(text))
	if err != nil {
		return fmt.Errorf("building ntfy request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := n.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending ntfy notification: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
