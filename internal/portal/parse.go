package portal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/briandealwis/bpw-dispatch/internal/state"
)

// ParseSchedule extracts a kid's morning/afternoon bus legs from a logged-in
// ChildTransportInfo page.
//
// Confirmed against saved copies of the real page from both findmyschool.ca
// and infobus.francobus.ca: the "Transport" section is an ASP.NET repeater,
// id-prefixed "MainContent_NestContent_rpTransportation", with one group per
// direction (group 0 = "To School"/"À l'école" = morning, group 1 = "From
// School"/"De l'école" = afternoon) and, within each group, a nested
// repeater with two legs (leg 0 = pickup, leg 1 = dropoff):
//
//	..._rTransportation_<group>_lblArrivalValue_<leg>        time
//	..._rTransportation_<group>_lblDetailLocationValue_<leg> stop/address
//	..._rTransportation_<group>_RunRouteInfo_<leg>            bus/route id
//
// Either group can be entirely absent (a kid who doesn't take the bus in
// that direction), in which case the corresponding Schedule leg is left nil.
func ParseSchedule(doc *goquery.Document) (*state.Schedule, error) {
	sched := &state.Schedule{}

	morning, err := parseGroup(doc, 0)
	if err != nil {
		return nil, err
	}
	afternoon, err := parseGroup(doc, 1)
	if err != nil {
		return nil, err
	}
	sched.Morning = morning
	sched.Afternoon = afternoon

	if sched.Morning == nil && sched.Afternoon == nil {
		return nil, fmt.Errorf("could not find a Transport section on the ChildTransportInfo page; the page layout may have changed — rerun with -debug and inspect debug/post-login.html")
	}
	return sched, nil
}

func idText(doc *goquery.Document, id string) string {
	return strings.TrimSpace(doc.Find("#" + id).First().Text())
}

// parseGroup reads one "To School"/"From School" transportation group
// (group 0 or 1), returning nil if that group isn't present on the page.
func parseGroup(doc *goquery.Document, group int) (*state.Leg, error) {
	prefix := "MainContent_NestContent_rpTransportation_rTransportation_" + strconv.Itoa(group)

	pickupTime := idText(doc, prefix+"_lblArrivalValue_0")
	pickupLocation := idText(doc, prefix+"_lblDetailLocationValue_0")
	bus := idText(doc, prefix+"_RunRouteInfo_0")
	dropoffTime := idText(doc, prefix+"_lblArrivalValue_1")
	dropoffLocation := idText(doc, prefix+"_lblDetailLocationValue_1")
	if bus == "" {
		bus = idText(doc, prefix+"_RunRouteInfo_1")
	}

	if pickupTime == "" && dropoffTime == "" && bus == "" {
		return nil, nil
	}
	return &state.Leg{
		Bus:             bus,
		PickupTime:      pickupTime,
		PickupLocation:  pickupLocation,
		DropoffTime:     dropoffTime,
		DropoffLocation: dropoffLocation,
	}, nil
}
