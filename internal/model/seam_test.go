package model

import (
	"testing"
	"time"
)

func TestMeterRangeMergeAndSubtract(t *testing.T) {
	merged := MergeMeterRanges([]MeterRange{{From: 10, To: 20}, {From: 15, To: 30}, {From: 40, To: 50}, {From: 0, To: 0}})
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged ranges, got %v", merged)
	}
	if merged[0] != (MeterRange{From: 10, To: 30}) || merged[1] != (MeterRange{From: 40, To: 50}) {
		t.Fatalf("unexpected merge result %v", merged)
	}
	// Adjacent ranges collapse into one continuous span.
	if adjacent := MergeMeterRanges([]MeterRange{{From: 10, To: 20}, {From: 20, To: 30}}); len(adjacent) != 1 || adjacent[0] != (MeterRange{From: 10, To: 30}) {
		t.Fatalf("adjacent ranges must merge, got %v", adjacent)
	}
	surviving := SubtractMeterRanges([]MeterRange{{From: 0, To: 100}}, []MeterRange{{From: 10, To: 20}, {From: 60, To: 70}})
	if len(surviving) != 3 {
		t.Fatalf("expected 3 surviving ranges, got %v", surviving)
	}
	want := []MeterRange{{From: 0, To: 10}, {From: 20, To: 60}, {From: 70, To: 100}}
	for index, span := range want {
		if surviving[index] != span {
			t.Fatalf("surviving range %d = %v, want %v", index, surviving[index], span)
		}
	}
	if got := SubtractMeterRanges([]MeterRange{{From: 0, To: 10}}, []MeterRange{{From: 0, To: 10}}); len(got) != 0 {
		t.Fatalf("expected empty remainder, got %v", got)
	}
}

func TestSeamCertificateValidation(t *testing.T) {
	base := SeamCertificate{
		ID:             NewEventID(),
		SeamID:         "seam-1",
		Revision:       1,
		JobID:          NewJobID(),
		JobGeneration:  1,
		RollGeneration: 1,
		Meter:          120,
		OverlapMM:      50,
		PullForceN:     200,
		Verdict:        SeamAccepted,
		InspectedAt:    time.Now().UTC(),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("accepted certificate should validate: %v", err)
	}
	rejected := base
	rejected.Verdict = SeamRejected
	rejected.RejectRange = MeterRange{From: 115, To: 125}
	if err := rejected.Validate(); err != nil {
		t.Fatalf("rejected certificate with range should validate: %v", err)
	}
	missingRange := rejected
	missingRange.RejectRange = MeterRange{}
	if err := missingRange.Validate(); err == nil {
		t.Fatal("rejected certificate without a reject range must fail validation")
	}
	carryingRange := base
	carryingRange.RejectRange = MeterRange{From: 1, To: 2}
	if err := carryingRange.Validate(); err == nil {
		t.Fatal("accepted certificate carrying a reject range must fail validation")
	}
	unknown := base
	unknown.Verdict = "maybe"
	if err := unknown.Validate(); err == nil {
		t.Fatal("unknown verdict must fail validation")
	}
}
