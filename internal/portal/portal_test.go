package portal

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func TestNewClient_RequestTimeout(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPClient.Timeout != 15*time.Second {
		t.Errorf("HTTPClient.Timeout = %s, want 15s", c.HTTPClient.Timeout)
	}
}

func loadDoc(t *testing.T, path string) *goquery.Document {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()
	doc, err := goquery.NewDocumentFromReader(f)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc
}

func TestFormValues_IgnoresThirdPartyOIDCButton(t *testing.T) {
	doc := loadDoc(t, "testdata/login-with-oidc.html")
	values, err := formValues(doc, "parent@example.com", "hunter2")
	if err != nil {
		t.Fatalf("formValues: %v", err)
	}

	if got := values.Get("ctl00$MainContent$glogin$lLogin$UserName"); got != "parent@example.com" {
		t.Errorf("username field = %q, want parent@example.com", got)
	}
	if got := values.Get("ctl00$MainContent$glogin$lLogin$Password"); got != "hunter2" {
		t.Errorf("password field = %q, want hunter2", got)
	}
	if got := values.Get("__EVENTTARGET"); got != "ctl00$MainContent$glogin$lLogin$btnLogin" {
		t.Errorf("__EVENTTARGET = %q, want the btnLogin control name", got)
	}
	if values.Has("provider") {
		t.Errorf("formValues submitted the third-party OIDC button (name=provider); it must never be submitted")
	}
	// Hidden ASP.NET fields must be carried over verbatim.
	if got := values.Get("__VIEWSTATE"); got != "FAKEVIEWSTATE123==" {
		t.Errorf("__VIEWSTATE = %q, want it preserved from the page", got)
	}
	// Unchecked checkbox must not be submitted.
	if values.Has("ctl00$MainContent$glogin$lLogin$RememberMe") {
		t.Errorf("unchecked RememberMe checkbox should not be submitted")
	}
	// Selects should carry their currently-selected option.
	if got := values.Get("ctl00$_cbDatabase"); got != "2409cbb4-234a-4208-8a7e-63763cdcb6dc" {
		t.Errorf("ctl00$_cbDatabase = %q, want the selected option's value", got)
	}
}

func TestFormValues_PlainLoginNoOIDC(t *testing.T) {
	doc := loadDoc(t, "testdata/login-plain.html")
	values, err := formValues(doc, "parent2@example.com", "s3cret")
	if err != nil {
		t.Fatalf("formValues: %v", err)
	}
	if got := values.Get("ctl00$MainContent$glogin$lLogin$UserName"); got != "parent2@example.com" {
		t.Errorf("username field = %q, want parent2@example.com", got)
	}
	if got := values.Get("ctl00$MainContent$glogin$lLogin$Password"); got != "s3cret" {
		t.Errorf("password field = %q, want s3cret", got)
	}
	if got := values.Get("__EVENTTARGET"); got != "ctl00$MainContent$glogin$lLogin$btnLogin" {
		t.Errorf("__EVENTTARGET = %q, want the btnLogin control name", got)
	}
}

func TestFormValues_NoForm(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<html><body>no form here</body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := formValues(doc, "u", "p"); err == nil {
		t.Fatal("expected an error when the page has no <form>")
	}
}
