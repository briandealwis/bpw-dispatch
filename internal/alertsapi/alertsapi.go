// Package alertsapi queries BusPlannerWeb's public (unauthenticated) Alerts
// JSON endpoints, as documented in https://github.com/briandealwis/bus-planner-skill.
package alertsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Alert is one row returned by the GetBusNotifications/GetSchoolNotifications
// endpoints. Field names match the JSON keys returned by the API.
type Alert struct {
	AppliesTo         string `json:"AppliesTo"`
	RouteRun          string `json:"RouteRun"`
	Action            string `json:"Action"`
	AffectsSchools    string `json:"AffectsSchools"`
	CreateTimeDisplay string `json:"CreateTimeDisplay"`
	Comment           string `json:"Comment"`
}

type notificationsResponse struct {
	D struct {
		Data []Alert `json:"data"`
	} `json:"d"`
}

// Client fetches alerts from a BusPlannerWeb domain.
type Client struct {
	HTTPClient *http.Client
}

// NewClient returns a Client with a sane request timeout.
func NewClient() *Client {
	return &Client{HTTPClient: &http.Client{Timeout: 20 * time.Second}}
}

// endpoint names, relative to https://<domain>/Alerts.aspx/
const (
	endpointBusNotifications = "GetBusNotifications"
)

func (c *Client) post(ctx context.Context, domain, endpoint string) ([]Alert, error) {
	url := fmt.Sprintf("https://%s/Alerts.aspx/%s", domain, endpoint)
	payload := map[string]any{
		"alertCondition": map[string]string{"RangeType": "0"},
		"dataTableData": map[string]any{
			"draw":          1,
			"length":        500,
			"start":         0,
			"columns":       []map[string]string{{"data": "RouteRun"}},
			"order":         []map[string]any{{"column": 0, "dir": "asc"}},
			"search":        map[string]string{"value": ""},
			"SortFieldName": "RouteRun",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bpw-dispatch/1.0)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s returned HTTP %d: %s", endpoint, resp.StatusCode, string(data))
	}

	var out notificationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding %s response: %w", endpoint, err)
	}
	return out.D.Data, nil
}

// FetchBusNotifications returns all active bus delay/cancellation alerts for
// a domain (unfiltered).
func (c *Client) FetchBusNotifications(ctx context.Context, domain string) ([]Alert, error) {
	return c.post(ctx, domain, endpointBusNotifications)
}

// MatchAlerts filters alerts to those matching the given bus and/or school
// substrings (case-insensitive). An empty substring matches everything.
func MatchAlerts(alerts []Alert, busSubstr, schoolSubstr string) []Alert {
	bus := strings.ToLower(strings.TrimSpace(busSubstr))
	school := strings.ToLower(strings.TrimSpace(schoolSubstr))
	var out []Alert
	for _, a := range alerts {
		if bus != "" && !strings.Contains(strings.ToLower(a.RouteRun), bus) {
			continue
		}
		if school != "" && !strings.Contains(strings.ToLower(a.AffectsSchools), school) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// FetchAndMatch fetches bus notifications for a domain and filters them to
// the given bus/school substrings.
func (c *Client) FetchAndMatch(ctx context.Context, domain, busSubstr, schoolSubstr string) ([]Alert, error) {
	alerts, err := c.FetchBusNotifications(ctx, domain)
	if err != nil {
		return nil, err
	}
	return MatchAlerts(alerts, busSubstr, schoolSubstr), nil
}
