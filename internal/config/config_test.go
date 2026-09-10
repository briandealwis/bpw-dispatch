package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const validConfig = `
notifiers:
  ntfy-parents:
    type: ntfy
    topic: topic1
  ntfy-grandma:
    type: ntfy
    topic: topic2

kids:
  - id: kid1
    school: Example School
    portal:
      domain: example.com
      username: user1
      password: pass1
    notifiers: [ntfy-parents]
    sessions:
      morning:
        days: [mon, tue, wed, thu, fri]
      afternoon:
        days: [mon, tue, wed, thu, fri]
        day_overrides:
          wed: [ntfy-grandma]
          fri: [ntfy-grandma]
`

func TestLoad_Valid(t *testing.T) {
	path := writeConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Kids) != 1 || cfg.Kids[0].ID != "kid1" {
		t.Fatalf("unexpected kids: %+v", cfg.Kids)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected an error for a missing config file")
	}
}

func TestValidate_NoKids(t *testing.T) {
	path := writeConfig(t, "notifiers: {}\nkids: []\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error when there are no kids")
	}
}

func TestValidate_MissingPortalFields(t *testing.T) {
	path := writeConfig(t, `
kids:
  - id: kid1
    school: Example School
    portal:
      domain: example.com
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error when portal username/password are missing")
	}
}

func TestValidate_UnknownNotifier(t *testing.T) {
	path := writeConfig(t, `
kids:
  - id: kid1
    school: Example School
    portal: {domain: example.com, username: u, password: p}
    notifiers: [does-not-exist]
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error when a kid references an unknown notifier")
	}
}

func TestValidate_UnknownSessionName(t *testing.T) {
	path := writeConfig(t, `
kids:
  - id: kid1
    school: Example School
    portal: {domain: example.com, username: u, password: p}
    sessions:
      lunch:
        days: [mon]
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for a session name other than morning/afternoon")
	}
}

func TestValidate_InvalidDay(t *testing.T) {
	path := writeConfig(t, `
kids:
  - id: kid1
    school: Example School
    portal: {domain: example.com, username: u, password: p}
    sessions:
      morning:
        days: [funday]
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an invalid day name")
	}
}

func TestValidate_DuplicateKidID(t *testing.T) {
	path := writeConfig(t, `
kids:
  - id: kid1
    school: A
    portal: {domain: a.com, username: u, password: p}
  - id: kid1
    school: B
    portal: {domain: b.com, username: u, password: p}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for duplicate kid ids")
	}
}

func TestKid_SessionActive(t *testing.T) {
	k := Kid{Sessions: map[string]SessionConfig{
		"morning":   {Days: []string{"mon", "wed"}},
		"afternoon": {}, // empty Days means every day
	}}
	cases := []struct {
		session, day string
		want         bool
	}{
		{"morning", "mon", true},
		{"morning", "tue", false},
		{"afternoon", "sun", true},
		{"evening", "mon", false}, // not configured at all
	}
	for _, c := range cases {
		if got := k.SessionActive(c.session, c.day); got != c.want {
			t.Errorf("SessionActive(%q, %q) = %v, want %v", c.session, c.day, got, c.want)
		}
	}
}

func TestKid_ResolveNotifiers(t *testing.T) {
	k := Kid{
		Notifiers: []string{"default-notifier"},
		Sessions: map[string]SessionConfig{
			"afternoon": {
				Notifiers:    []string{"afternoon-notifier"},
				DayOverrides: map[string][]string{"wed": {"grandma"}, "fri": {"grandma"}},
			},
			"morning": {}, // no override, falls back to kid.Notifiers
		},
	}

	if got := k.ResolveNotifiers("afternoon", "wed"); len(got) != 1 || got[0] != "grandma" {
		t.Errorf("afternoon/wed notifiers = %v, want [grandma]", got)
	}
	if got := k.ResolveNotifiers("afternoon", "mon"); len(got) != 1 || got[0] != "afternoon-notifier" {
		t.Errorf("afternoon/mon notifiers = %v, want [afternoon-notifier]", got)
	}
	if got := k.ResolveNotifiers("morning", "mon"); len(got) != 1 || got[0] != "default-notifier" {
		t.Errorf("morning/mon notifiers = %v, want [default-notifier]", got)
	}
	if got := k.ResolveNotifiers("evening", "mon"); got != nil {
		t.Errorf("evening notifiers = %v, want nil (session not configured)", got)
	}
}
