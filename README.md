# bpw-dispatch

A small CLI, meant to run every few minutes from cron, that checks one or
more [BusPlannerWeb](https://www.busplanner.com) parent portals
(`findmyschool.ca`, `infobus.francobus.ca`, and similar boards' portals) for
schedule changes and bus alerts, and notifies you — without needing to log
in to each portal by hand.

Each run:

1. Loads `config.yaml` and the cached state file.
2. For each configured kid, logs in to their parent portal **at most once
   per day** to check their bus/pickup/dropoff schedule. If it changed
   since the last check, sends an alert.
3. Checks the (unauthenticated) Alerts API for that kid's bus, for the
   current morning/afternoon session. Sends an alert on any change,
   **including the first check of a session even when there's nothing
   wrong** — you get one "operating as scheduled" message per session,
   then only updates.
4. Alert text never includes the child's name — only school, bus, and the
   kid's scheduled time for the session (the pickup in the morning, the
   drop-off back home in the afternoon), e.g.:
   `École élémentaire L'Odyssée, Route 140: Bus delayed by 10-15 minutes (pickup 7:58 AM)`
   or `École élémentaire L'Odyssée, Route 140: Operating as scheduled (drop-off 3:53 PM)`
5. Each ntfy.sh notification is sent with a click-through link (ntfy's
   `X-Click` action) to the relevant portal page for details: the Alerts
   page for bus-status messages, the ChildTransportInfo page for
   schedule-change messages. Tapping the notification opens it directly.
6. Every network call (portal login, alerts API, ntfy) has a **15s
   timeout**, and transient failures (timeouts, DNS and connection errors,
   HTTP 5xx/429) are **retried up to 5 times** with exponential backoff
   (1s, 2s, 4s, 8s, 16s), giving up after a minute per call. Permanent
   failures (e.g. a rejected login, an HTTP 4xx) aren't retried.
7. When a check still fails, it's handled in two tiers so a flaky portal
   doesn't spam you:
   - It's sent straight away to the **`error_notifiers`** channel (once
     per distinct error, plus a "recovered" message when it's working
     again).
   - The regular notifiers **keep the result of the last successful
     check** — no error, and no repeated "Operating as scheduled" when it
     recovers. They're only told once the check has been failing for
     longer than **`stale_after`** (default 15 minutes), e.g.
     `Example School, 140: Unable to check bus status since 07:05 (timed
     out)`. After that, the next successful check is sent as an all-clear.

   Failures are classified so the message names the likely cause:
   `DNS lookup for www.findmyschool.ca timed out`, `connection refused`,
   `TLS handshake failed`, `server returned HTTP 503`, or a generic
   `timed out`.

## Building

```sh
go build -o bpw-dispatch ./cmd/bpw-dispatch
```

For a NAS running ARMv7 (32-bit ARM), cross-compile from a dev machine:

```sh
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o bpw-dispatch-arm7 ./cmd/bpw-dispatch
```

Copy the resulting binary to the NAS; it's statically linked, so nothing
else needs to be installed there.

## Configuring

Copy `config.example.yaml` to `config.yaml` and fill in real credentials.
Keep `config.yaml` out of version control — it holds portal passwords.

```yaml
state_file: state.json
stale_after: 15m                    # how long a check must keep failing before
                                     # the regular notifiers are told
error_notifiers: [ntfy-errors]      # where every failed check is reported

notifiers:
  ntfy-parents:
    type: ntfy
    topic: bpw-dispatch-changeme1   # pick a hard-to-guess topic; anyone who
                                     # knows it can read your notifications
  ntfy-errors:
    type: ntfy
    topic: bpw-dispatch-changeme-errors

kids:
  - id: kid1
    school: "École élémentaire L'Odyssée"
    bus_label: "Route 140"          # optional friendly name used in alerts
    portal:
      domain: www.findmyschool.ca
      username: parent@example.com
      password: "..."
    alert_match:
      bus: "140"                    # substring matched against the Alerts
                                     # API's RouteRun field (see below)
    notifiers: [ntfy-parents]
    sessions:
      morning:
        days: [mon, tue, wed, thu, fri]
      afternoon:
        days: [mon, tue, wed, thu, fri]
        day_overrides:               # e.g. grandma picks up Wed/Fri
          wed: [ntfy-grandma]
          fri: [ntfy-grandma]
```

See `config.example.yaml` for a complete two-kid example, including the
grandma-picks-up-on-Wed/Fri case.

### Verifying `alert_match.bus`

The Alerts API's `RouteRun` field format varies by school board (e.g.
`"140 (SHG_001)"` vs `"K500: DSJ500_PU"`). If `alert_match.bus` is left
unset, bpw-dispatch falls back to whatever bus/route identifier it scraped
from the portal that day, which may or may not be a substring of a real
alert's `RouteRun`. Once you've seen one real alert (or can trigger a test
one), check it matches, and set `alert_match.bus` explicitly if not.

### Notifiers

Only [ntfy.sh](https://ntfy.sh) is implemented today (`type: ntfy`). WhatsApp
and iMessage were considered but dropped from v1: WhatsApp has no safe
first-party API for this use case (the official Business API doesn't fit a
personal script, and unofficial libraries risk the account), and iMessage
has no API reachable from a Linux ARM NAS at all (it needs an always-on Mac
as a bridge). The `notify.Notifier` interface is small on purpose, so
another backend can be added later without touching the rest of the app.

## Running

```sh
./bpw-dispatch -config config.yaml
```

Flags:

- `-config` — path to the YAML config (default `config.yaml`)
- `-state` — override the state file path (default: config's `state_file`,
  or `state.json`)
- `-debug` — dump fetched portal HTML to `-debug-dir` (default `debug/`),
  for troubleshooting if login or schedule-scraping ever breaks against a
  real site (e.g. after a BusPlannerWeb redesign)
- `-verbose` — log every step to stderr with timestamps: which kid is being
  processed, whether the schedule/alerts need checking, and a line before
  *and* after every network call (portal login, alerts API, ntfy send) with
  its duration. Use this if a run seems to hang or take too long — the last
  "starting" line with no matching result line is exactly where it's stuck.

### Cron

```
*/5 6-9,14-17 * * 1-5 /path/to/bpw-dispatch -config /path/to/config.yaml -state /path/to/state.json >> /path/to/bpw-dispatch.log 2>&1
```

(Restricting the cron window to school hours isn't required — the program
only does real work once per session — but it avoids pointless runs at 2am.)

## How schedule scraping works

The parent portal's ChildTransportInfo page (`internal/portal/parse.go`) is
scraped by fixed element IDs, e.g.
`MainContent_NestContent_rpTransportation_rTransportation_0_lblArrivalValue_0`.
This was verified against real pages from both `findmyschool.ca` and
`infobus.francobus.ca` — both run the same BusPlannerWeb product with
identical control IDs, just localized text — so it should hold for other
BusPlannerWeb-hosted boards too. If a board's page differs, run with
`-debug` and compare `debug/post-login.html` against the IDs in
`internal/portal/parse.go`.

Login (`internal/portal/portal.go`) replicates the real "Log In" button's
behavior: it's an ASP.NET WebForms `__doPostBack`, not a plain form submit.
Some boards (e.g. via UGDSB) also show a third-party OIDC "Returning ...
parent login" button on the same page — that button is never submitted;
only the site's own username/password login is used.

## Testing

```sh
go test ./...
```

Portal login/parsing tests run against synthetic fixtures under
`internal/portal/testdata/` that mirror the real BusPlannerWeb page
structure (verified against real saved pages) with all data fabricated —
real fixtures aren't checked in since the pages carry a child's name,
student number, and home address.
