// Package app orchestrates one run of bpw-dispatch: for each configured
// kid, refresh the daily schedule (alerting on changes), then check for bus
// alerts for the current morning/afternoon session (alerting on any change,
// including the first "operating as scheduled" check of the session).
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
	"github.com/briandealwis/bpw-dispatch/internal/portal"
	"github.com/briandealwis/bpw-dispatch/internal/session"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

// ScheduleFetcher logs in to a parent portal and returns a kid's current
// schedule. Implemented by *portal.Client; kept as an interface here so App
// can be tested with a fake.
type ScheduleFetcher interface {
	FetchSchedule(ctx context.Context, domain, username, password string) (*state.Schedule, error)
}

// AlertsFetcher fetches and filters bus alerts for a domain. Implemented by
// *alertsapi.Client; kept as an interface here so App can be tested with a
// fake.
type AlertsFetcher interface {
	FetchAndMatch(ctx context.Context, domain, busSubstr, schoolSubstr string) ([]alertsapi.Alert, error)
}

// App holds the wired-up dependencies for one run.
type App struct {
	Config    *config.Config
	State     *state.State
	Alerts    AlertsFetcher
	Portal    ScheduleFetcher
	Notifiers map[string]notify.Notifier
	Now       func() time.Time
}

// New builds an App from config and state, constructing the alerts/portal
// clients and the notifier registry.
func New(cfg *config.Config, st *state.State) (*App, error) {
	notifiers, err := notify.Build(cfg.Notifiers)
	if err != nil {
		return nil, err
	}
	portalClient, err := portal.NewClient()
	if err != nil {
		return nil, fmt.Errorf("building portal client: %w", err)
	}
	return &App{
		Config:    cfg,
		State:     st,
		Alerts:    alertsapi.NewClient(),
		Portal:    portalClient,
		Notifiers: notifiers,
		Now:       time.Now,
	}, nil
}

// Run processes every configured kid once. Errors for individual kids are
// collected and returned together; one kid's failure doesn't stop the rest.
func (a *App) Run(ctx context.Context) error {
	now := a.Now()
	today := session.DateKey(now)
	sess := session.Current(now)
	weekday := session.WeekdayName(now)

	var errs []error
	for _, kid := range a.Config.Kids {
		if err := a.runKid(ctx, kid, today, sess, weekday); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *App) runKid(ctx context.Context, kid config.Kid, today string, sess session.Session, weekday string) error {
	ks := a.State.Kids[kid.ID]
	if ks == nil {
		ks = &state.KidState{}
		a.State.Kids[kid.ID] = ks
	}

	var errs []error

	if ks.ScheduleDate != today {
		newSched, err := a.Portal.FetchSchedule(ctx, kid.Portal.Domain, kid.Portal.Username, kid.Portal.Password)
		if err != nil {
			errs = append(errs, fmt.Errorf("kid %s: fetching schedule: %w", kid.ID, err))
		} else {
			if ks.Schedule != nil && !ks.Schedule.Equal(newSched) {
				a.send(ctx, kid.Notifiers, formatScheduleChange(kid, ks.Schedule, newSched), portalScheduleURL(kid.Portal.Domain), &errs)
			}
			ks.Schedule = newSched
			ks.ScheduleDate = today
		}
	}

	// Discard alert-tracking state from a previous day or a previous
	// session (e.g. the morning session's state, once it's afternoon).
	if ks.Session == nil || ks.Session.Date != today || ks.Session.Name != string(sess) {
		ks.Session = &state.SessionState{Date: today, Name: string(sess)}
	}

	if !kid.SessionActive(string(sess), weekday) {
		return errors.Join(errs...)
	}
	notifierNames := kid.ResolveNotifiers(string(sess), weekday)
	if len(notifierNames) == 0 {
		return errors.Join(errs...)
	}

	leg := scheduleLeg(ks.Schedule, sess)
	busFilter := kid.AlertMatch.Bus
	if busFilter == "" && leg != nil {
		busFilter = leg.Bus
	}

	alerts, err := a.Alerts.FetchAndMatch(ctx, kid.Portal.Domain, busFilter, kid.AlertMatch.School)
	if err != nil {
		errs = append(errs, fmt.Errorf("kid %s: fetching alerts: %w", kid.ID, err))
		return errors.Join(errs...)
	}

	msg := formatAlertMessage(kid, leg, alerts)
	if !ks.Session.Sent || msg != ks.Session.LastMessage {
		a.send(ctx, notifierNames, msg, portalAlertsURL(kid.Portal.Domain), &errs)
		ks.Session.LastMessage = msg
		ks.Session.Sent = true
	}

	return errors.Join(errs...)
}

func (a *App) send(ctx context.Context, notifierNames []string, text, clickURL string, errs *[]error) {
	msg := notify.Message{Text: text, ClickURL: clickURL}
	for _, name := range notifierNames {
		n, ok := a.Notifiers[name]
		if !ok {
			*errs = append(*errs, fmt.Errorf("notifier %q not found", name))
			continue
		}
		if err := n.Send(ctx, msg); err != nil {
			*errs = append(*errs, fmt.Errorf("sending via %q: %w", name, err))
		}
	}
}

// portalScheduleURL is where a parent can see a kid's current pickup/dropoff
// schedule in detail, linked from schedule-change alerts.
func portalScheduleURL(domain string) string {
	return "https://" + domain + "/Subscriptions/ChildTransportInfo"
}

// portalAlertsURL is the portal's Alerts page, linked from bus-alert messages.
func portalAlertsURL(domain string) string {
	return "https://" + domain + "/Alerts"
}

func scheduleLeg(sched *state.Schedule, sess session.Session) *state.Leg {
	if sched == nil {
		return nil
	}
	if sess == session.Morning {
		return sched.Morning
	}
	return sched.Afternoon
}
