// Command bpw-dispatch checks BusPlannerWeb parent portals for schedule
// changes and bus alerts, meant to be invoked periodically (e.g. every 5
// minutes via cron).
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/briandealwis/bpw-dispatch/internal/app"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/portal"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	statePath := flag.String("state", "", "path to the state file (default: config's state_file, or state.json)")
	debug := flag.Bool("debug", false, "dump fetched portal HTML for troubleshooting login/scraping")
	debugDir := flag.String("debug-dir", "debug", "directory for -debug HTML dumps")
	insecure := flag.Bool("insecure", false, "skip TLS certificate verification (use when the portal's cert is expired)")
	flag.Parse()

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

	st, err := state.Load(sp)
	if err != nil {
		log.Fatalf("loading state: %v", err)
	}

	a, err := app.New(cfg, st)
	if err != nil {
		log.Fatalf("initializing: %v", err)
	}
	if pc, ok := a.Portal.(*portal.Client); ok {
		pc.Debug = *debug
		pc.DebugDir = *debugDir
		if *insecure {
			pc.HTTPClient.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			}
		}
	}

	runErr := a.Run(context.Background())
	if runErr != nil {
		log.Printf("completed with errors: %v", runErr)
	}

	if err := state.Save(sp, st); err != nil {
		log.Fatalf("saving state: %v", err)
	}

	if runErr != nil {
		os.Exit(1)
	}
}
