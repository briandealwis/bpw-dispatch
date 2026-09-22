// Package portal logs in to a BusPlannerWeb parent portal and scrapes a
// kid's ChildTransportInfo page for their current bus/pickup/dropoff details.
//
// The login page is ASP.NET WebForms (confirmed against a saved copy of
// findmyschool.ca's Login page). The "Log In" button is a plain
// type="button" wired to __doPostBack(...) rather than a real submit
// input, so logging in means replicating that postback: submit every
// hidden field verbatim (__VIEWSTATE etc.), fill in the username/password
// controls, and set __EVENTTARGET to the login button's control name.
//
// The page also carries a genuine type="submit" button for a third-party
// UGDSB OIDC login ("provider" / "Returning UGDSB parent login"); that
// button is deliberately never submitted here — only the site's own
// username/password login is used.
//
// francobus.ca is assumed to run the same BusPlannerWeb product with the
// same control IDs; enable Debug to dump fetched HTML for inspection if
// that assumption doesn't hold.
package portal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/briandealwis/bpw-dispatch/internal/logging"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

// Client logs in to BusPlannerWeb portals and fetches schedule pages.
type Client struct {
	HTTPClient *http.Client
	// Debug, when true, dumps fetched HTML pages under DebugDir for
	// troubleshooting login/scraping against the real site.
	Debug    bool
	DebugDir string
	// Log, when set, receives a line before and after every HTTP request
	// this Client makes — the "before" line lets a hung run be traced back
	// to exactly which request never returned.
	Log logging.Logger
}

// requestTimeout bounds every HTTP request this package makes, so a slow or
// unresponsive portal fails fast enough that its failure can be reported
// (see formatStatusMessage) rather than the whole run stalling past the
// next cron tick.
const requestTimeout = 15 * time.Second

// NewClient returns a Client with its own cookie jar (required to carry the
// login session from the login POST to the ChildTransportInfo GET).
func NewClient() (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("creating cookie jar: %w", err)
	}
	return &Client{
		HTTPClient: &http.Client{Jar: jar, Timeout: requestTimeout},
		DebugDir:   "debug",
	}, nil
}

// do performs req and logs a line before sending it and a line after it
// returns (with status+duration, or the error). The "before" line is what
// lets a hung run be traced to exactly which request is stuck: if a run
// never logs the matching "after" line, that request is where it's hanging.
func (c *Client) do(client *http.Client, req *http.Request) (*http.Response, error) {
	start := time.Now()
	logging.Logf(c.Log, "portal: %s %s starting", req.Method, req.URL)
	resp, err := client.Do(req)
	if err != nil {
		logging.Logf(c.Log, "portal: %s %s failed after %s: %v", req.Method, req.URL, time.Since(start), err)
		return nil, err
	}
	logging.Logf(c.Log, "portal: %s %s -> %d in %s", req.Method, req.URL, resp.StatusCode, time.Since(start))
	return resp, nil
}

func (c *Client) dump(name string, r io.Reader) {
	if !c.Debug {
		return
	}
	if err := os.MkdirAll(c.DebugDir, 0o755); err != nil {
		return
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.DebugDir, name), data, 0o600)
}

// FetchSchedule returns the current bus schedule for the logged-in child.
//
// If cachedToken (a previously saved BPWebAuth cookie value) is non-empty,
// it is tried first — skipping the full login round-trip. On failure (token
// expired or invalid) the full login flow is performed automatically.
//
// The returned token is the BPWebAuth value to persist for the next call; it
// may equal cachedToken (if the cached token was still valid) or be a freshly
// issued value (after a new login).
func (c *Client) FetchSchedule(ctx context.Context, domain, username, password, cachedToken string) (*state.Schedule, string, error) {
	logging.Logf(c.Log, "portal %s: FetchSchedule starting (cached token present: %v)", domain, cachedToken != "")
	if cachedToken != "" {
		sched, err := c.fetchWithToken(ctx, domain, cachedToken)
		if err == nil {
			logging.Logf(c.Log, "portal %s: cached token still valid, skipping login", domain)
			return sched, cachedToken, nil
		}
		logging.Logf(c.Log, "portal %s: cached token rejected (%v), falling back to full login", domain, err)
	}
	return c.loginAndFetch(ctx, domain, username, password)
}

// fetchWithToken GETs the ChildTransportInfo page using only the BPWebAuth
// cookie. Returns an error if the server responds with a redirect (token
// expired or unknown). Redirects are not followed so the 3xx can be detected
// reliably.
func (c *Client) fetchWithToken(ctx context.Context, domain, token string) (*state.Schedule, error) {
	pageURL := fmt.Sprintf("https://%s/Subscriptions/ChildTransportInfo.aspx", domain)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", "BPWebAuth="+token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bpw-dispatch/1.0)")

	// Don't follow redirects: a 3xx response means the token is invalid.
	noRedirectClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       c.HTTPClient.Timeout,
		Transport:     c.HTTPClient.Transport,
	}

	resp, err := c.do(noRedirectClient, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 == 3 {
		return nil, fmt.Errorf("auth token expired (server returned %d %s)", resp.StatusCode, resp.Status)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	c.dump("childtransportinfo.html", strings.NewReader(string(bodyBytes)))

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("parsing schedule page: %w", err)
	}
	return ParseSchedule(doc)
}

// loginAndFetch performs the full ASP.NET WebForms login, then parses the
// ChildTransportInfo page returned after the login redirect. It returns the
// schedule and the BPWebAuth token issued by the server for future reuse.
func (c *Client) loginAndFetch(ctx context.Context, domain, username, password string) (*state.Schedule, string, error) {
	loginURL := fmt.Sprintf(
		"https://%s/Login?ReturnUrl=%%2fSubscriptions%%2fChildTransportInfo.aspx&LoginType=Subscriber&showParentPopup=False",
		domain,
	)

	doc, action, err := c.fetchForm(ctx, loginURL, "login-page.html")
	if err != nil {
		return nil, "", fmt.Errorf("fetching login page: %w", err)
	}

	values, err := formValues(doc, username, password)
	if err != nil {
		return nil, "", fmt.Errorf("reading login form: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, action, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("building login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bpw-dispatch/1.0)")

	resp, err := c.do(c.HTTPClient, req)
	if err != nil {
		return nil, "", fmt.Errorf("submitting login: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading login response: %w", err)
	}
	c.dump("post-login.html", strings.NewReader(string(bodyBytes)))

	finalURL := resp.Request.URL.String()
	if !strings.Contains(finalURL, "ChildTransportInfo") {
		return nil, "", fmt.Errorf(
			"login did not reach ChildTransportInfo (ended at %s); check credentials, or the login form fields may have changed — rerun with -debug to inspect debug/login-page.html and debug/post-login.html",
			finalURL,
		)
	}

	// Extract the BPWebAuth token the server just issued.
	var token string
	domainURL, _ := url.Parse("https://" + domain)
	for _, cookie := range c.HTTPClient.Jar.Cookies(domainURL) {
		if cookie.Name == "BPWebAuth" {
			token = cookie.Value
			break
		}
	}

	scheduleDoc, err := goquery.NewDocumentFromReader(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, "", fmt.Errorf("parsing schedule page: %w", err)
	}
	sched, err := ParseSchedule(scheduleDoc)
	return sched, token, err
}

// fetchForm GETs a page and returns its parsed document plus the resolved
// (absolute) action URL of its first <form>.
func (c *Client) fetchForm(ctx context.Context, pageURL, dumpName string) (*goquery.Document, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bpw-dispatch/1.0)")

	resp, err := c.do(c.HTTPClient, req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	c.dump(dumpName, strings.NewReader(string(bodyBytes)))

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, "", err
	}

	form := doc.Find("form").First()
	action, _ := form.Attr("action")
	if action == "" {
		return doc, resp.Request.URL.String(), nil
	}
	resolved, err := resp.Request.URL.Parse(action)
	if err != nil {
		return nil, "", fmt.Errorf("resolving form action %q: %w", action, err)
	}
	return doc, resolved.String(), nil
}

// formValues builds the POST body that replicates a click on the
// BusPlannerWeb login button: every hidden field is carried over verbatim
// (ASP.NET's __VIEWSTATE etc.), every <select> keeps its currently-selected
// option, the username/password controls (matched by control-name
// substring, e.g. "...glogin$lLogin$UserName") are filled in, and
// __EVENTTARGET is set to the login button's control name so the postback
// is recognized as that click.
//
// Any type="submit"/type="image" input is deliberately ignored — some
// BusPlannerWeb sites (e.g. findmyschool.ca) carry a genuine submit button
// for a third-party board OIDC login ("provider" / UGDSB) alongside the
// button-triggered postback login; only the site's own username/password
// login is ever submitted here.
func formValues(doc *goquery.Document, username, password string) (url.Values, error) {
	form := doc.Find("form").First()
	if form.Length() == 0 {
		return nil, fmt.Errorf("no <form> found on login page")
	}

	values := url.Values{}
	var usernameField, loginButtonName string

	form.Find("input").Each(func(_ int, s *goquery.Selection) {
		name, ok := s.Attr("name")
		if !ok || name == "" {
			return
		}
		typ := strings.ToLower(s.AttrOr("type", "text"))
		lname := strings.ToLower(name)
		val, _ := s.Attr("value")

		switch typ {
		case "hidden":
			values.Set(name, val)
		case "password":
			if strings.Contains(lname, "login") {
				values.Set(name, password)
			}
		case "checkbox", "radio":
			if _, checked := s.Attr("checked"); checked {
				values.Set(name, val)
			}
		case "text", "email":
			if strings.Contains(lname, "username") {
				values.Set(name, username)
				usernameField = name
			}
		case "button":
			if strings.Contains(lname, "btnlogin") {
				loginButtonName = name
			}
			// "submit"/"image" (e.g. a third-party OIDC provider button)
			// and anything else are intentionally not submitted.
		}
	})

	form.Find("select").Each(func(_ int, s *goquery.Selection) {
		name, ok := s.Attr("name")
		if !ok || name == "" {
			return
		}
		selected := s.Find("option[selected]").First()
		if selected.Length() == 0 {
			selected = s.Find("option").First()
		}
		val, _ := selected.Attr("value")
		values.Set(name, val)
	})

	if usernameField == "" {
		return nil, fmt.Errorf("could not find the login username field on the login form (site layout may have changed)")
	}
	if loginButtonName == "" {
		return nil, fmt.Errorf("could not find the Log In button on the login form (site layout may have changed)")
	}
	values.Set("__EVENTTARGET", loginButtonName)
	values.Set("__EVENTARGUMENT", "")
	return values, nil
}
