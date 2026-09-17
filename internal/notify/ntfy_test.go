package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/briandealwis/bpw-dispatch/internal/config"
)

func TestNtfy_Send(t *testing.T) {
	var gotPath, gotBody, gotClick string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotClick = r.Header.Get("X-Click")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNtfy(config.Notifier{Type: "ntfy", Server: srv.URL, Topic: "my-topic"})
	err := n.Send(context.Background(), Message{Text: "hello world", ClickURL: "https://example.com/Alerts"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/my-topic" {
		t.Errorf("path = %q, want /my-topic", gotPath)
	}
	if gotBody != "hello world" {
		t.Errorf("body = %q, want %q", gotBody, "hello world")
	}
	if gotClick != "https://example.com/Alerts" {
		t.Errorf("X-Click = %q, want https://example.com/Alerts", gotClick)
	}
}

func TestNtfy_Send_NoClickURL(t *testing.T) {
	var gotClick string
	gotClickSet := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClick, gotClickSet = r.Header.Get("X-Click"), r.Header.Get("X-Click") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNtfy(config.Notifier{Server: srv.URL, Topic: "t"})
	if err := n.Send(context.Background(), Message{Text: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotClickSet {
		t.Errorf("expected no X-Click header when ClickURL is unset, got %q", gotClick)
	}
}

func TestNtfy_Send_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	n := NewNtfy(config.Notifier{Server: srv.URL, Topic: "t"})
	if err := n.Send(context.Background(), Message{Text: "hi"}); err == nil {
		t.Fatal("expected an error for a non-2xx response")
	}
}

func TestNewNtfy_DefaultServer(t *testing.T) {
	n := NewNtfy(config.Notifier{Topic: "t"})
	if n.Server != "https://ntfy.sh" {
		t.Errorf("default server = %q, want https://ntfy.sh", n.Server)
	}
}

func TestBuild_UnsupportedType(t *testing.T) {
	_, err := Build(map[string]config.Notifier{"x": {Type: "whatsapp"}})
	if err == nil {
		t.Fatal("expected an error for an unsupported notifier type")
	}
}
