// Package state persists per-kid schedule and alert-tracking data between runs.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// Leg is one direction's bus/pickup/dropoff details, as scraped from the
// parent portal's "To School" or "From School" transportation group.
type Leg struct {
	Bus             string `json:"bus"`
	PickupTime      string `json:"pickup_time"`
	PickupLocation  string `json:"pickup_location"`
	DropoffTime     string `json:"dropoff_time"`
	DropoffLocation string `json:"dropoff_location"`
}

// Equal reports whether two legs (including nil) are the same.
func (l *Leg) Equal(o *Leg) bool {
	if l == nil || o == nil {
		return l == o
	}
	return *l == *o
}

// Schedule is a kid's morning (to school) and afternoon (from school) bus
// legs, as scraped from the parent portal. Either leg may be absent (a kid
// might only take the bus in one direction).
type Schedule struct {
	Morning   *Leg `json:"morning,omitempty"`
	Afternoon *Leg `json:"afternoon,omitempty"`
}

// Equal reports whether two schedules (including nil) are the same.
func (s *Schedule) Equal(o *Schedule) bool {
	if s == nil || o == nil {
		return s == o
	}
	return s.Morning.Equal(o.Morning) && s.Afternoon.Equal(o.Afternoon)
}

// SessionState tracks what's already been alerted for the current morning/
// afternoon session, so unchanged results aren't re-sent.
type SessionState struct {
	Date        string `json:"date"` // YYYY-MM-DD
	Name        string `json:"name"` // "morning" or "afternoon"
	LastMessage string `json:"last_message"`
	Sent        bool   `json:"sent"`
	// Failing is set once this session's notifiers have been told the alerts
	// check is failing, so it's reported once rather than on every run, and
	// so the next successful check is sent as an all-clear.
	Failing bool `json:"failing,omitempty"`
}

// Failure tracks an ongoing streak of failed checks of one kind (schedule
// refresh or alerts check) for a kid. It's nil while checks are succeeding.
type Failure struct {
	// Since is when the current streak of failures started.
	Since time.Time `json:"since"`
	// LastError is the last failure message sent to the error notifiers, so
	// an unchanged failure isn't re-sent every run.
	LastError string `json:"last_error,omitempty"`
	// MainNotified records that the kid's regular notifiers were told about
	// this streak (used for schedule failures; alerts failures track this
	// per session via SessionState.Failing).
	MainNotified bool `json:"main_notified,omitempty"`
}

// KidState is the persisted state for a single kid.
type KidState struct {
	Schedule        *Schedule     `json:"schedule,omitempty"`
	ScheduleDate    string        `json:"schedule_date,omitempty"` // YYYY-MM-DD schedule was last fetched
	Session         *SessionState `json:"session,omitempty"`
	AuthToken       string        `json:"auth_token,omitempty"` // cached BPWebAuth cookie for skipping login
	ScheduleFailure *Failure      `json:"schedule_failure,omitempty"`
	AlertsFailure   *Failure      `json:"alerts_failure,omitempty"`
}

// State is the full contents of the on-disk state file, keyed by kid id.
type State struct {
	Kids map[string]*KidState `json:"kids"`
}

// Load reads the state file, returning an empty State if it doesn't yet exist.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{Kids: map[string]*KidState{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	if s.Kids == nil {
		s.Kids = map[string]*KidState{}
	}
	return &s, nil
}

// Save writes the state file atomically (write to a temp file, then rename).
func Save(path string, s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("saving state file: %w", err)
	}
	return nil
}
