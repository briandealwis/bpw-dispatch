package portal

import "testing"

func TestParseSchedule_BothLegs(t *testing.T) {
	doc := loadDoc(t, "testdata/childtransportinfo-both-legs.html")
	sched, err := ParseSchedule(doc)
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	if sched.Morning == nil {
		t.Fatal("expected a morning leg")
	}
	m := sched.Morning
	if m.Bus != "140" {
		t.Errorf("morning bus = %q, want 140", m.Bus)
	}
	if m.PickupTime != "7:58 AM" || m.PickupLocation != "ELM ST @ PINE ST" {
		t.Errorf("morning pickup = %q at %q, want 7:58 AM at ELM ST @ PINE ST", m.PickupTime, m.PickupLocation)
	}
	if m.DropoffTime != "8:45 AM" || m.DropoffLocation != "EXAMPLE SCHOOL - BUS LOADING ZONE" {
		t.Errorf("morning dropoff = %q at %q, want 8:45 AM at EXAMPLE SCHOOL - BUS LOADING ZONE", m.DropoffTime, m.DropoffLocation)
	}

	if sched.Afternoon == nil {
		t.Fatal("expected an afternoon leg")
	}
	a := sched.Afternoon
	if a.Bus != "140" {
		t.Errorf("afternoon bus = %q, want 140", a.Bus)
	}
	if a.PickupTime != "3:30 PM" || a.PickupLocation != "EXAMPLE SCHOOL - BUS LOADING ZONE" {
		t.Errorf("afternoon pickup = %q at %q, want 3:30 PM at EXAMPLE SCHOOL - BUS LOADING ZONE", a.PickupTime, a.PickupLocation)
	}
	if a.DropoffTime != "3:53 PM" || a.DropoffLocation != "ELM ST @ PINE ST" {
		t.Errorf("afternoon dropoff = %q at %q, want 3:53 PM at ELM ST @ PINE ST", a.DropoffTime, a.DropoffLocation)
	}
}

func TestParseSchedule_MorningOnly(t *testing.T) {
	doc := loadDoc(t, "testdata/childtransportinfo-morning-only.html")
	sched, err := ParseSchedule(doc)
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	if sched.Morning == nil {
		t.Fatal("expected a morning leg")
	}
	if sched.Morning.Bus != "K500: DSJ500_PU" {
		t.Errorf("morning bus = %q, want K500: DSJ500_PU", sched.Morning.Bus)
	}
	if sched.Afternoon != nil {
		t.Errorf("expected no afternoon leg, got %+v", sched.Afternoon)
	}
}

func TestParseSchedule_NoTransportSection(t *testing.T) {
	doc := loadDoc(t, "testdata/login-plain.html") // a page with no transport repeater at all
	if _, err := ParseSchedule(doc); err == nil {
		t.Fatal("expected an error when no transport section is present")
	}
}
