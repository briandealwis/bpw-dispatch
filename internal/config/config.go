package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level YAML configuration for bpw-dispatch.
type Config struct {
	StateFile string              `yaml:"state_file"`
	Notifiers map[string]Notifier `yaml:"notifiers"`
	Kids      []Kid               `yaml:"kids"`
}

// Notifier describes one configured alert destination.
type Notifier struct {
	Type   string `yaml:"type"` // currently only "ntfy" is implemented
	Server string `yaml:"server"`
	Topic  string `yaml:"topic"`
}

// PortalConfig holds the BusPlannerWeb parent-portal login for one kid.
type PortalConfig struct {
	Domain   string `yaml:"domain"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// AlertMatch controls how alerts fetched from the Alerts API are matched to this kid.
// Bus and School are case-insensitive substrings matched against the alert's
// RouteRun / AffectsSchools fields respectively. If Bus is empty, the bus number
// last scraped from the parent portal schedule is used instead.
type AlertMatch struct {
	Bus    string `yaml:"bus"`
	School string `yaml:"school"`
}

// SessionConfig configures one of a kid's two daily sessions (morning/afternoon).
type SessionConfig struct {
	// Days this session is active, e.g. ["mon","tue","wed","thu","fri"].
	// Empty means every day.
	Days []string `yaml:"days"`
	// Notifiers to use for this session, overriding the kid's default Notifiers.
	Notifiers []string `yaml:"notifiers"`
	// DayOverrides lets specific weekdays use different notifiers than the
	// session default (e.g. grandma's ntfy topic on Wed/Fri afternoons only).
	DayOverrides map[string][]string `yaml:"day_overrides"`
}

// Kid is one child's portal credentials, school/bus identity, and alert schedule.
type Kid struct {
	ID     string `yaml:"id"`
	School string `yaml:"school"`
	// BusLabel is the friendly bus name used in alert text (e.g. "Route 140").
	// Defaults to the bus/route identifier scraped from the parent portal.
	BusLabel   string                   `yaml:"bus_label"`
	Portal     PortalConfig             `yaml:"portal"`
	AlertMatch AlertMatch               `yaml:"alert_match"`
	Notifiers  []string                 `yaml:"notifiers"`
	Sessions   map[string]SessionConfig `yaml:"sessions"`
}

var validDays = map[string]bool{
	"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true,
}

var validSessionNames = map[string]bool{"morning": true, "afternoon": true}

// SessionActive reports whether the given session ("morning"/"afternoon") is
// configured and active on the given weekday (e.g. "wed").
func (k Kid) SessionActive(sessionName, weekday string) bool {
	sc, ok := k.Sessions[sessionName]
	if !ok {
		return false
	}
	if len(sc.Days) == 0 {
		return true
	}
	for _, d := range sc.Days {
		if d == weekday {
			return true
		}
	}
	return false
}

// ResolveNotifiers returns the notifier names to use for this kid's session on
// the given weekday, applying any day-specific override.
func (k Kid) ResolveNotifiers(sessionName, weekday string) []string {
	sc, ok := k.Sessions[sessionName]
	if !ok {
		return nil
	}
	if n, ok := sc.DayOverrides[weekday]; ok {
		return n
	}
	if len(sc.Notifiers) > 0 {
		return sc.Notifiers
	}
	return k.Notifiers
}

// Load reads and validates a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config yaml: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the config for internal consistency (references between
// kids/sessions and notifiers, valid day names, required fields).
func (c *Config) Validate() error {
	if len(c.Kids) == 0 {
		return fmt.Errorf("config has no kids defined")
	}
	for name, n := range c.Notifiers {
		if n.Type == "" {
			return fmt.Errorf("notifier %q: type is required", name)
		}
	}
	checkNotifier := func(context, name string) error {
		if _, ok := c.Notifiers[name]; !ok {
			return fmt.Errorf("%s references unknown notifier %q", context, name)
		}
		return nil
	}
	seen := map[string]bool{}
	for _, k := range c.Kids {
		if k.ID == "" {
			return fmt.Errorf("kid entry missing required 'id'")
		}
		if seen[k.ID] {
			return fmt.Errorf("duplicate kid id %q", k.ID)
		}
		seen[k.ID] = true
		if k.School == "" {
			return fmt.Errorf("kid %q missing required 'school'", k.ID)
		}
		if k.Portal.Domain == "" || k.Portal.Username == "" || k.Portal.Password == "" {
			return fmt.Errorf("kid %q: portal.domain, portal.username and portal.password are all required", k.ID)
		}
		for _, n := range k.Notifiers {
			if err := checkNotifier(fmt.Sprintf("kid %q", k.ID), n); err != nil {
				return err
			}
		}
		for sessName, sc := range k.Sessions {
			if !validSessionNames[sessName] {
				return fmt.Errorf("kid %q: unknown session %q (must be \"morning\" or \"afternoon\")", k.ID, sessName)
			}
			for _, d := range sc.Days {
				if !validDays[d] {
					return fmt.Errorf("kid %q session %q: invalid day %q", k.ID, sessName, d)
				}
			}
			for _, n := range sc.Notifiers {
				if err := checkNotifier(fmt.Sprintf("kid %q session %q", k.ID, sessName), n); err != nil {
					return err
				}
			}
			for day, ns := range sc.DayOverrides {
				if !validDays[day] {
					return fmt.Errorf("kid %q session %q: invalid day_override day %q", k.ID, sessName, day)
				}
				for _, n := range ns {
					if err := checkNotifier(fmt.Sprintf("kid %q session %q day_override %q", k.ID, sessName, day), n); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
