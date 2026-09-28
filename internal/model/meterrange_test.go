package model

import "testing"

func TestMergeMeterRangesUnion(t *testing.T) {
	merged := MergeMeterRanges([]MeterRange{{Start: 10, End: 12}, {Start: 5, End: 8}, {Start: 7, End: 11}, {Start: 20, End: 20}})
	want := []MeterRange{{Start: 5, End: 12}}
	if len(merged) != 1 {
		t.Fatalf("expected a single merged range, got %v", merged)
	}
	if merged[0] != want[0] {
		t.Fatalf("expected %v, got %v", want[0], merged[0])
	}
}

func TestMergeMeterRangesKeepsDisjointSorted(t *testing.T) {
	merged := MergeMeterRanges([]MeterRange{{Start: 30, End: 35}, {Start: 0, End: 4}})
	if len(merged) != 2 || merged[0].Start != 0 || merged[1].Start != 30 {
		t.Fatalf("expected two disjoint sorted ranges, got %v", merged)
	}
}

func TestMeterRangeContainsAndOverlaps(t *testing.T) {
	rejected := MeterRange{Start: 198, End: 202}
	if !rejected.Contains(200) || rejected.Contains(202) || rejected.Contains(197.9) {
		t.Fatal("Contains did not honor the half-open [start,end) interval")
	}
	if !rejected.Overlaps(MeterRange{Start: 201, End: 250}) {
		t.Fatal("expected overlap with downstream lot")
	}
	if rejected.Overlaps(MeterRange{Start: 202, End: 250}) {
		t.Fatal("touching endpoints must not overlap")
	}
	if rejected.Overlaps(MeterRange{Start: 200, End: 200}) {
		t.Fatal("zero-length lot cannot overlap")
	}
}
