package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

// TestFetchSchedule_LoginAndScrape runs the full FetchSchedule flow (GET
// login page, POST credentials, follow redirect, parse schedule) against a
// local server that mimics BusPlannerWeb's login postback: it only accepts
// requests carrying the same fields a browser click on the real "Log In"
// button would submit.
func TestFetchSchedule_LoginAndScrape(t *testing.T) {
	loginHTML, err := os.ReadFile("testdata/login-plain.html")
	if err != nil {
		t.Fatal(err)
	}
	scheduleHTML, err := os.ReadFile("testdata/childtransportinfo-both-legs.html")
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/Login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write(loginHTML)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("server: parsing login POST form: %v", err)
		}
		if got, want := r.Form.Get("ctl00$MainContent$glogin$lLogin$UserName"), "parent@example.com"; got != want {
			t.Errorf("server: username = %q, want %q", got, want)
		}
		if got, want := r.Form.Get("ctl00$MainContent$glogin$lLogin$Password"), "hunter2"; got != want {
			t.Errorf("server: password = %q, want %q", got, want)
		}
		if got, want := r.Form.Get("__EVENTTARGET"), "ctl00$MainContent$glogin$lLogin$btnLogin"; got != want {
			t.Errorf("server: __EVENTTARGET = %q, want %q", got, want)
		}
		if r.Form.Has("provider") {
			t.Error("server: received a provider (OIDC) field; login must not submit it")
		}
		http.Redirect(w, r, "/Subscriptions/ChildTransportInfo", http.StatusFound)
	})
	mux.HandleFunc("/Subscriptions/ChildTransportInfo", func(w http.ResponseWriter, r *http.Request) {
		w.Write(scheduleHTML)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	client.HTTPClient.CheckRedirect = nil // use default (follow) redirect behavior

	domain := srv.URL[len("http://"):]
	// FetchSchedule always builds an https:// URL; point it at our http
	// test server instead by overriding the transport to rewrite scheme.
	client.HTTPClient.Transport = rewriteHTTPSTransport{target: srv.URL}

	sched, err := client.FetchSchedule(context.Background(), domain, "parent@example.com", "hunter2")
	if err != nil {
		t.Fatalf("FetchSchedule: %v", err)
	}
	if sched.Morning == nil || sched.Morning.Bus != "140" {
		t.Errorf("unexpected schedule: %+v", sched)
	}
}

// rewriteHTTPSTransport redirects any https:// request to the given http://
// test-server base, so tests can exercise the real https:// URL-building
// code in FetchSchedule against an httptest.Server (which is plain HTTP).
type rewriteHTTPSTransport struct {
	target string
}

func (t rewriteHTTPSTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	req.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
