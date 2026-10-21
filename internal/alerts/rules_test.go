package alerts

import (
	"math"
	"testing"
	"time"
)

func standard() Rule {
	return Rule{Name: "CPU busy", Metric: "cpu", Direction: "above",
		FireAt: 85, RecoverAt: 70, WindowSeconds: 300, MinSamples: 3, MaxAgeSeconds: 120}
}
func ptr(v float64) *float64 { return &v }
func TestThresholdHysteresis(t *testing.T) {
	r := standard()
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		v                  float64
		previous           bool
		status, transition string
		firing             bool
	}{
		{84.9, false, "normal", "none", false},
		{85, false, "firing", "fired", true},
		{80, true, "firing", "none", true},
		{70.01, true, "firing", "none", true},
		{70, true, "normal", "recovered", false},
		{0, false, "normal", "none", false},
	}
	for _, tt := range tests {
		out := Evaluate(r, tt.previous, Observation{Average: ptr(tt.v), Samples: 3, Latest: &at}, at)
		if out.Status != tt.status || out.Transition != tt.transition || out.Firing != tt.firing {
			t.Errorf("value=%v prior=%v: %+v", tt.v, tt.previous, out)
		}
	}
}
func TestBelowAndUnknownHoldState(t *testing.T) {
	r := standard()
	r.Direction, r.FireAt, r.RecoverAt = "below", 15, 25
	at := time.Now().UTC()
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	out := Evaluate(r, false, Observation{Average: ptr(15), Samples: 3, Latest: &at}, at)
	if !out.Firing || out.Transition != "fired" {
		t.Fatal(out)
	}
	out = Evaluate(r, true, Observation{Average: ptr(20), Samples: 3, Latest: &at}, at)
	if !out.Firing || out.Transition != "none" {
		t.Fatal(out)
	}
	out = Evaluate(r, true, Observation{Average: ptr(25), Samples: 3, Latest: &at}, at)
	if out.Firing || out.Transition != "recovered" {
		t.Fatal(out)
	}
	old := at.Add(-3 * time.Minute)
	for _, obs := range []Observation{
		{Samples: 3, Latest: &at},
		{Average: ptr(0), Samples: 2, Latest: &at},
		{Average: ptr(0), Samples: 4, Latest: &old},
		{Average: ptr(math.NaN()), Samples: 4, Latest: &at},
	} {
		out = Evaluate(r, true, obs, at)
		if out.Status != "insufficient_data" || !out.Firing || out.Transition != "none" {
			t.Fatal(out)
		}
	}
}
func TestRuleValidation(t *testing.T) {
	r := standard()
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	bads := []Rule{
		{Metric: "cpu"},
		{Name: "bad", Metric: "network", Direction: "above", FireAt: 90, RecoverAt: 80, WindowSeconds: 60, MinSamples: 1, MaxAgeSeconds: 30},
		{Name: "bad", Metric: "cpu", Direction: "above", FireAt: 80, RecoverAt: 80, WindowSeconds: 60, MinSamples: 1, MaxAgeSeconds: 30},
		{Name: "bad", Metric: "cpu", Direction: "below", FireAt: 80, RecoverAt: 40, WindowSeconds: 60, MinSamples: 1, MaxAgeSeconds: 30},
		{Name: "bad", Metric: "cpu", Direction: "above", FireAt: math.NaN(), RecoverAt: 10, WindowSeconds: 60, MinSamples: 1, MaxAgeSeconds: 30},
	}
	for _, bad := range bads {
		if Validate(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	r.Metric, r.DiskPath = "disk", "/"
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	r.DiskPath = ""
	if Validate(r) == nil {
		t.Fatal("disk path required")
	}
}
