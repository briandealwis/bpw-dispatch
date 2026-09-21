// Command bpw-dispatch checks BusPlannerWeb parent portals for schedule
// changes and bus alerts, meant to be invoked periodically (e.g. every 5
// minutes via cron).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/app"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/logging"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
	"github.com/briandealwis/bpw-dispatch/internal/portal"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	statePath := flag.String("state", "", "path to the state file (default: config's state_file, or state.json)")
	debug := flag.Bool("debug", false, "dump fetched portal HTML for troubleshooting login/scraping")
	debugDir := flag.String("debug-dir", "debug", "directory for -debug HTML dumps")
	verbose := flag.Bool("verbose", false, "log every step and network call to stderr, with timestamps — use this to see where a run is stuck or slow")
	flag.Parse()

	verboseLog := logging.Discard
	if *verbose {
		verboseLog = logging.New(os.Stderr)
	}

	logging.Logf(verboseLog, "loading config from %s", *configPath)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	sp := cfg.StateFile
	if *statePath != "" {
		sp = *statePath
	}
	if sp == "" {
		sp = "state.json"
	}

	logging.Logf(verboseLog, "loading state from %s", sp)
	st, err := state.Load(sp)
	if err != nil {
		log.Fatalf("loading state: %v", err)
	}

	a, err := app.New(cfg, st)
	if err != nil {
		log.Fatalf("initializing: %v", err)
	}
	a.Log = verboseLog
	if pc, ok := a.Portal.(*portal.Client); ok {
		pc.Debug = *debug
		pc.DebugDir = *debugDir
		pc.Log = verboseLog
	}
	if ac, ok := a.Alerts.(*alertsapi.Client); ok {
		ac.Log = verboseLog
	}
	for _, n := range a.Notifiers {
		if nn, ok := n.(*notify.Ntfy); ok {
			nn.Log = verboseLog
		}
	}

	start := time.Now()
	runErr := a.Run(context.Background())
	logging.Logf(verboseLog, "run took %s", time.Since(start))
	if runErr != nil {
		log.Printf("completed with errors: %v", runErr)
	}

	logging.Logf(verboseLog, "saving state to %s", sp)
	if err := state.Save(sp, st); err != nil {
		log.Fatalf("saving state: %v", err)
	}

	if runErr != nil {
		os.Exit(1)
	}
}
