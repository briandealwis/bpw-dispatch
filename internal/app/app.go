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
	"github.com/briandealwis/bpw-dispatch/internal/logging"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
	"github.com/briandealwis/bpw-dispatch/internal/portal"
	"github.com/briandealwis/bpw-dispatch/internal/session"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

// ScheduleFetcher logs in to a parent portal and returns a kid's current
// schedule. Implemented by *portal.Client; kept as an interface here so App
// can be tested with a fake.
//
// cachedToken is a BPWebAuth cookie value saved from a previous call; pass
// the empty string on the first call. The returned token should be stored
// in state and passed back on the next call to avoid a full login round-trip.
type ScheduleFetcher interface {
	FetchSchedule(ctx context.Context, domain, username, password, cachedToken string) (*state.Schedule, string, error)
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
	// Log, when set, receives a line at each step of Run — which kid is
	// being processed, whether the schedule/alerts need checking, and what
	// was (or wasn't) sent — so a run that hangs or takes too long can be
	// traced to where it got stuck.
	Log logging.Logger
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
	logging.Logf(a.Log, "run: starting for %s, session=%s, weekday=%s, %d kid(s)", today, sess, weekday, len(a.Config.Kids))

	var errs []error
	for _, kid := range a.Config.Kids {
		logging.Logf(a.Log, "kid %s: starting", kid.ID)
		start := time.Now()
		err := a.runKid(ctx, kid, today, sess, weekday)
		if err != nil {
			errs = append(errs, err)
			logging.Logf(a.Log, "kid %s: finished with error after %s: %v", kid.ID, time.Since(start), err)
		} else {
			logging.Logf(a.Log, "kid %s: finished after %s", kid.ID, time.Since(start))
		}
	}
	logging.Logf(a.Log, "run: done")
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
		logging.Logf(a.Log, "kid %s: schedule not yet checked today, fetching", kid.ID)
		newSched, newToken, err := a.Portal.FetchSchedule(ctx, kid.Portal.Domain, kid.Portal.Username, kid.Portal.Password, ks.AuthToken)
		if err != nil {
			logging.Logf(a.Log, "kid %s: schedule fetch failed: %v", kid.ID, err)
			errs = append(errs, fmt.Errorf("kid %s: fetching schedule: %w", kid.ID, err))
		} else {
			logging.Logf(a.Log, "kid %s: schedule fetched", kid.ID)
			if ks.Schedule != nil && !ks.Schedule.Equal(newSched) {
				logging.Logf(a.Log, "kid %s: schedule changed, sending alert to %v", kid.ID, kid.Notifiers)
				a.send(ctx, kid.Notifiers, formatScheduleChange(kid, ks.Schedule, newSched), portalScheduleURL(kid.Portal.Domain), &errs)
			}
			ks.Schedule = newSched
			ks.ScheduleDate = today
			ks.AuthToken = newToken
		}
	} else {
		logging.Logf(a.Log, "kid %s: schedule already checked today, skipping", kid.ID)
	}

	// Discard alert-tracking state from a previous day or a previous
	// session (e.g. the morning session's state, once it's afternoon).
	if ks.Session == nil || ks.Session.Date != today || ks.Session.Name != string(sess) {
		ks.Session = &state.SessionState{Date: today, Name: string(sess)}
	}

	if !kid.SessionActive(string(sess), weekday) {
		logging.Logf(a.Log, "kid %s: session %s not active on %s, skipping alerts check", kid.ID, sess, weekday)
		return errors.Join(errs...)
	}
	notifierNames := kid.ResolveNotifiers(string(sess), weekday)
	if len(notifierNames) == 0 {
		logging.Logf(a.Log, "kid %s: no notifiers resolved for session %s on %s, skipping alerts check", kid.ID, sess, weekday)
		return errors.Join(errs...)
	}

	leg := scheduleLeg(ks.Schedule, sess)
	busFilter := kid.AlertMatch.Bus
	if busFilter == "" && leg != nil {
		busFilter = leg.Bus
	}

	logging.Logf(a.Log, "kid %s: checking alerts (bus filter=%q, school filter=%q)", kid.ID, busFilter, kid.AlertMatch.School)
	alerts, err := a.Alerts.FetchAndMatch(ctx, kid.Portal.Domain, busFilter, kid.AlertMatch.School)
	if err != nil {
		logging.Logf(a.Log, "kid %s: alerts fetch failed: %v", kid.ID, err)
		errs = append(errs, fmt.Errorf("kid %s: fetching alerts: %w", kid.ID, err))
		return errors.Join(errs...)
	}
	logging.Logf(a.Log, "kid %s: got %d matching alert(s)", kid.ID, len(alerts))

	msg := formatAlertMessage(kid, leg, alerts)
	if !ks.Session.Sent || msg != ks.Session.LastMessage {
		logging.Logf(a.Log, "kid %s: message changed, sending to %v", kid.ID, notifierNames)
		a.send(ctx, notifierNames, msg, portalAlertsURL(kid.Portal.Domain), &errs)
		ks.Session.LastMessage = msg
		ks.Session.Sent = true
	} else {
		logging.Logf(a.Log, "kid %s: message unchanged, not sending", kid.ID)
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
		logging.Logf(a.Log, "notifier %s: sending starting", name)
		if err := n.Send(ctx, msg); err != nil {
			logging.Logf(a.Log, "notifier %s: sending failed: %v", name, err)
			*errs = append(*errs, fmt.Errorf("sending via %q: %w", name, err))
		} else {
			logging.Logf(a.Log, "notifier %s: sent", name)
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
