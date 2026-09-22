package portal

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

const testBPWebAuthToken = "test-bpwebauth-token-value"

// buildTestServer returns an httptest.Server that mimics BusPlannerWeb's
// login postback and ChildTransportInfo serving. The login POST handler
// validates form fields and sets a BPWebAuth cookie before redirecting.
// The ChildTransportInfo handler serves scheduleHTML if the BPWebAuth cookie
// is present, and redirects to Login if not.
func buildTestServer(t *testing.T, loginHTML, scheduleHTML []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	handleSchedule := func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("BPWebAuth")
		if err != nil || cookie.Value != testBPWebAuthToken {
			http.Redirect(w, r, "/Login", http.StatusFound)
			return
		}
		w.Write(scheduleHTML)
	}
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
		http.SetCookie(w, &http.Cookie{Name: "BPWebAuth", Value: testBPWebAuthToken, Path: "/"})
		http.Redirect(w, r, "/Subscriptions/ChildTransportInfo.aspx", http.StatusFound)
	})
	// Handle both with and without .aspx: loginAndFetch follows the server's
	// redirect (which goes to .aspx), fetchWithToken requests .aspx directly.
	mux.HandleFunc("/Subscriptions/ChildTransportInfo.aspx", handleSchedule)
	mux.HandleFunc("/Subscriptions/ChildTransportInfo", handleSchedule)
	return httptest.NewServer(mux)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	client.HTTPClient.CheckRedirect = nil
	client.HTTPClient.Transport = rewriteHTTPSTransport{target: srv.URL}
	return client
}

// TestFetchSchedule_LoginAndScrape verifies the full login flow: GET login
// page, POST credentials (including correct __EVENTTARGET), follow redirect
// to ChildTransportInfo, parse schedule. Also checks that the returned token
// matches the BPWebAuth cookie set by the server.
func TestFetchSchedule_LoginAndScrape(t *testing.T) {
	loginHTML, err := os.ReadFile("testdata/login-plain.html")
	if err != nil {
		t.Fatal(err)
	}
	scheduleHTML, err := os.ReadFile("testdata/childtransportinfo-both-legs.html")
	if err != nil {
		t.Fatal(err)
	}

	srv := buildTestServer(t, loginHTML, scheduleHTML)
	defer srv.Close()

	client := newTestClient(t, srv)
	domain := srv.URL[len("http://"):]

	sched, token, err := client.FetchSchedule(context.Background(), domain, "parent@example.com", "hunter2", "")
	if err != nil {
		t.Fatalf("FetchSchedule: %v", err)
	}
	if sched.Morning == nil || sched.Morning.Bus != "140" {
		t.Errorf("unexpected schedule: %+v", sched)
	}
	if token != testBPWebAuthToken {
		t.Errorf("returned token = %q, want %q", token, testBPWebAuthToken)
	}
}

// TestFetchSchedule_CachedTokenSkipsLogin verifies that a valid cached token
// bypasses the login form entirely and fetches the schedule in one request.
func TestFetchSchedule_CachedTokenSkipsLogin(t *testing.T) {
	loginHTML, err := os.ReadFile("testdata/login-plain.html")
	if err != nil {
		t.Fatal(err)
	}
	scheduleHTML, err := os.ReadFile("testdata/childtransportinfo-both-legs.html")
	if err != nil {
		t.Fatal(err)
	}

	loginCalled := false
	mux := http.NewServeMux()
	handleSchedule2 := func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("BPWebAuth")
		if err != nil || cookie.Value != testBPWebAuthToken {
			http.Redirect(w, r, "/Login", http.StatusFound)
			return
		}
		w.Write(scheduleHTML)
	}
	mux.HandleFunc("/Login", func(w http.ResponseWriter, r *http.Request) {
		loginCalled = true
		if r.Method == http.MethodGet {
			w.Write(loginHTML)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "BPWebAuth", Value: testBPWebAuthToken, Path: "/"})
		http.Redirect(w, r, "/Subscriptions/ChildTransportInfo.aspx", http.StatusFound)
	})
	mux.HandleFunc("/Subscriptions/ChildTransportInfo.aspx", handleSchedule2)
	mux.HandleFunc("/Subscriptions/ChildTransportInfo", handleSchedule2)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv)
	domain := srv.URL[len("http://"):]

	sched, token, err := client.FetchSchedule(context.Background(), domain, "parent@example.com", "hunter2", testBPWebAuthToken)
	if err != nil {
		t.Fatalf("FetchSchedule with cached token: %v", err)
	}
	if loginCalled {
		t.Error("login endpoint was called even though a valid cached token was provided")
	}
	if sched.Morning == nil || sched.Morning.Bus != "140" {
		t.Errorf("unexpected schedule: %+v", sched)
	}
	if token != testBPWebAuthToken {
		t.Errorf("token should be unchanged when cached token was valid, got %q", token)
	}
}

// TestFetchSchedule_ExpiredTokenFallsBackToLogin verifies that an invalid
// cached token triggers a full login rather than returning an error.
func TestFetchSchedule_ExpiredTokenFallsBackToLogin(t *testing.T) {
	loginHTML, err := os.ReadFile("testdata/login-plain.html")
	if err != nil {
		t.Fatal(err)
	}
	scheduleHTML, err := os.ReadFile("testdata/childtransportinfo-both-legs.html")
	if err != nil {
		t.Fatal(err)
	}

	srv := buildTestServer(t, loginHTML, scheduleHTML)
	defer srv.Close()

	client := newTestClient(t, srv)
	domain := srv.URL[len("http://"):]

	sched, token, err := client.FetchSchedule(context.Background(), domain, "parent@example.com", "hunter2", "expired-token")
	if err != nil {
		t.Fatalf("FetchSchedule with expired token: %v", err)
	}
	if sched.Morning == nil || sched.Morning.Bus != "140" {
		t.Errorf("unexpected schedule after fallback login: %+v", sched)
	}
	if token != testBPWebAuthToken {
		t.Errorf("expected fresh token after login fallback, got %q", token)
	}
}

// TestFetchSchedule_TimesOutRatherThanHanging verifies that a portal which
// accepts a connection but never responds (as opposed to refusing it, or a
// slow-but-eventually-responding server) doesn't hang the run forever: the
// configured HTTPClient.Timeout still applies and FetchSchedule returns a
// timeout error. The Timeout is overridden to a short value here purely so
// this test doesn't take the real 15s (NewClient's default) to run; the
// mechanism under test is the same either way.
func TestFetchSchedule_TimesOutRatherThanHanging(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn // accept but never write a response
		}
	}()

	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	client.HTTPClient.Timeout = 200 * time.Millisecond
	client.HTTPClient.Transport = rewriteHTTPSTransport{target: "http://" + ln.Addr().String()}

	start := time.Now()
	_, _, err = client.FetchSchedule(context.Background(), "example.com", "u", "p", "")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error from a portal that never responds")
	}
	if elapsed > 2*time.Second {
		t.Errorf("FetchSchedule took %s; the configured Timeout doesn't seem to be applied", elapsed)
	}
	var te interface{ Timeout() bool }
	if !errors.As(err, &te) || !te.Timeout() {
		t.Errorf("error %v does not report itself as a timeout", err)
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
