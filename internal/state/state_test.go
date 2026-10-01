package state

import (
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileReturnsEmptyState(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Kids == nil || len(st.Kids) != 0 {
		t.Fatalf("expected an empty, non-nil Kids map, got %+v", st.Kids)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := &State{Kids: map[string]*KidState{
		"kid1": {
			ScheduleDate: "2026-09-10",
			Schedule: &Schedule{
				Morning: &Leg{Bus: "140", PickupTime: "7:58 AM", PickupLocation: "ELM ST"},
			},
			Session: &SessionState{Date: "2026-09-10", Name: "morning", LastMessage: "all good", Sent: true},
		},
	}}
	if err := Save(path, st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ks := got.Kids["kid1"]
	if ks == nil {
		t.Fatal("kid1 missing after roundtrip")
	}
	if ks.ScheduleDate != "2026-09-10" {
		t.Errorf("ScheduleDate = %q", ks.ScheduleDate)
	}
	if ks.Schedule == nil || ks.Schedule.Morning == nil || ks.Schedule.Morning.Bus != "140" {
		t.Errorf("Schedule not preserved: %+v", ks.Schedule)
	}
	if ks.Session == nil || !ks.Session.Sent || ks.Session.LastMessage != "all good" {
		t.Errorf("Session not preserved: %+v", ks.Session)
	}
}

func TestLegEqual(t *testing.T) {
	a := &Leg{Bus: "140", PickupTime: "7:58 AM"}
	b := &Leg{Bus: "140", PickupTime: "7:58 AM"}
	c := &Leg{Bus: "141", PickupTime: "7:58 AM"}

	if !a.Equal(b) {
		t.Error("identical legs should be equal")
	}
	if a.Equal(c) {
		t.Error("legs with different bus should not be equal")
	}
	var nilLeg *Leg
	if !nilLeg.Equal(nil) {
		t.Error("nil.Equal(nil) should be true")
	}
	if nilLeg.Equal(a) || a.Equal(nilLeg) {
		t.Error("nil should never equal a non-nil leg")
	}
}

func TestScheduleEqual(t *testing.T) {
	s1 := &Schedule{Morning: &Leg{Bus: "140"}, Afternoon: &Leg{Bus: "140"}}
	s2 := &Schedule{Morning: &Leg{Bus: "140"}, Afternoon: &Leg{Bus: "140"}}
	s3 := &Schedule{Morning: &Leg{Bus: "141"}, Afternoon: &Leg{Bus: "140"}}
	s4 := &Schedule{Morning: &Leg{Bus: "140"}, Afternoon: nil}

	if !s1.Equal(s2) {
		t.Error("identical schedules should be equal")
	}
	if s1.Equal(s3) {
		t.Error("schedules with a different morning bus should not be equal")
	}
	if s1.Equal(s4) {
		t.Error("schedules where one has a nil leg should not be equal")
	}
	var nilSched *Schedule
	if !nilSched.Equal(nil) {
		t.Error("nil.Equal(nil) should be true")
	}
}

func TestSameStop(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"YORKSHIRE ST S & PRESTON ST", "PRESTON ST & YORKSHIRE ST S", true},
		{"BRISTOL ST @ RAYMOND ST", "raymond st @ bristol st", true},
		{"BRISTOL ST @ RAYMOND ST", "RAYMOND ST & BRISTOL ST", true},
		{"David Saint-Jacques (7 et 8)  226-215-3325", "David Saint-Jacques (7 et 8) 226-215-3325", true},
		{"YORKSHIRE ST S & PRESTON ST", "YORKSHIRE ST S & WATERLOO AVE", false},
		{"MAIN ST", "ELM ST", false},
		{"", "", true},
	}
	for _, c := range cases {
		if got := SameStop(c.a, c.b); got != c.want {
			t.Errorf("SameStop(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestLegEqual_IgnoresReorderedIntersection(t *testing.T) {
	a := &Leg{Bus: "K500", PickupTime: "07:25", PickupLocation: "YORKSHIRE ST S & PRESTON ST"}
	b := &Leg{Bus: "K500", PickupTime: "07:25", PickupLocation: "PRESTON ST & YORKSHIRE ST S"}
	if !a.Equal(b) {
		t.Error("legs differing only in the order of an intersection's streets should be equal")
	}
}
