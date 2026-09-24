package alertsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/retry"
)

func TestNewClient_RequestTimeout(t *testing.T) {
	c := NewClient()
	if c.HTTPClient.Timeout != 15*time.Second {
		t.Errorf("HTTPClient.Timeout = %s, want 15s", c.HTTPClient.Timeout)
	}
}

func TestMatchAlerts(t *testing.T) {
	alerts := []Alert{
		{RouteRun: "140 (SHG_001)", AffectsSchools: "Sacred Heart CS (Guelph)", Action: "Bus Delayed"},
		{RouteRun: "K500: DSJ500_PU", AffectsSchools: "David Saint-Jacques", Action: "Route Cancelled"},
	}

	cases := []struct {
		name        string
		bus, school string
		wantActions []string
	}{
		{"bus substring case-insensitive", "shg_001", "", []string{"Bus Delayed"}},
		{"school substring", "", "david", []string{"Route Cancelled"}},
		{"both filters, only one matches", "140", "sacred", []string{"Bus Delayed"}},
		{"no match", "999", "", nil},
		{"empty filters match everything", "", "", []string{"Bus Delayed", "Route Cancelled"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MatchAlerts(alerts, c.bus, c.school)
			if len(got) != len(c.wantActions) {
				t.Fatalf("got %d alerts, want %d: %+v", len(got), len(c.wantActions), got)
			}
			for i, a := range got {
				if a.Action != c.wantActions[i] {
					t.Errorf("alert[%d].Action = %q, want %q", i, a.Action, c.wantActions[i])
				}
			}
		})
	}
}

func TestFetchBusNotifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Alerts.aspx/GetBusNotifications" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %q", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if _, ok := body["alertCondition"]; !ok {
			t.Error("request body missing alertCondition")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"d":{"recordsTotal":1,"data":[{"RouteRun":"140","Action":"Bus Delayed - 10 to 19 minutes","AffectsSchools":"Sacred Heart CS"}]}}`))
	}))
	defer srv.Close()

	client := NewClient()
	domain := srv.URL[len("http://"):]
	client.HTTPClient.Transport = &schemeRewriteTransport{scheme: "http"}

	alerts, err := client.FetchBusNotifications(context.Background(), domain)
	if err != nil {
		t.Fatalf("FetchBusNotifications: %v", err)
	}
	if len(alerts) != 1 || alerts[0].Action != "Bus Delayed - 10 to 19 minutes" {
		t.Errorf("unexpected alerts: %+v", alerts)
	}
}

func TestFetchBusNotifications_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewClient()
	domain := srv.URL[len("http://"):]
	client.HTTPClient.Transport = &schemeRewriteTransport{scheme: "http"}

	_, err := client.FetchBusNotifications(context.Background(), domain)
	var se *retry.StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusInternalServerError {
		t.Fatalf("err = %v, want a *retry.StatusError with status 500", err)
	}
	if !retry.IsTransient(err) {
		t.Error("a 500 from the alerts API should be treated as transient (retryable)")
	}
}

// schemeRewriteTransport rewrites https:// requests to http:// so tests can
// exercise the real https://<domain>/... URL-building code in the client
// against a plain-HTTP httptest.Server.
type schemeRewriteTransport struct{ scheme string }

func (t *schemeRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.scheme
	return http.DefaultTransport.RoundTrip(req)
}
