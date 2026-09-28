package model

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

type SeamVerdict string

const (
	SeamAccepted SeamVerdict = "accepted"
	SeamRejected SeamVerdict = "rejected"
)

// MeterRange is a half-open web meter interval [From, To) in the roll coordinate.
type MeterRange struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

func (meterRange MeterRange) Valid() bool {
	return meterRange.To > meterRange.From
}

func (meterRange MeterRange) Overlaps(other MeterRange) bool {
	return meterRange.Valid() && other.Valid() && meterRange.From < other.To && other.From < meterRange.To
}

// MergeMeterRanges folds overlapping or adjacent ranges into a minimal sorted set.
func MergeMeterRanges(ranges []MeterRange) []MeterRange {
	ordered := make([]MeterRange, 0, len(ranges))
	for _, meterRange := range ranges {
		if meterRange.Valid() {
			ordered = append(ordered, meterRange)
		}
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].From == ordered[right].From {
			return ordered[left].To < ordered[right].To
		}
		return ordered[left].From < ordered[right].From
	})
	merged := make([]MeterRange, 0, len(ordered))
	for _, meterRange := range ordered {
		if len(merged) == 0 || meterRange.From > merged[len(merged)-1].To {
			merged = append(merged, meterRange)
			continue
		}
		if meterRange.To > merged[len(merged)-1].To {
			merged[len(merged)-1].To = meterRange.To
		}
	}
	return merged
}

// SubtractMeterRanges removes the union of rejected from accepted, returning the surviving ranges.
func SubtractMeterRanges(accepted, rejected []MeterRange) []MeterRange {
	surviving := MergeMeterRanges(accepted)
	for _, cut := range MergeMeterRanges(rejected) {
		next := make([]MeterRange, 0, len(surviving))
		for _, span := range surviving {
			if !span.Overlaps(cut) {
				next = append(next, span)
				continue
			}
			if span.From < cut.From {
				next = append(next, MeterRange{From: span.From, To: cut.From})
			}
			if cut.To < span.To {
				next = append(next, MeterRange{From: cut.To, To: span.To})
			}
		}
		surviving = next
	}
	return surviving
}

// SeamCertificate is the durable splice-seam inspection record. Reinspections are
// appended as new revisions; a certificate is never mutated or deleted.
type SeamCertificate struct {
	ID             EventID     `json:"id"`
	SeamID         string      `json:"seam_id"`
	Revision       int         `json:"revision"`
	JobID          JobID       `json:"job_id"`
	JobGeneration  uint64      `json:"job_generation"`
	RollGeneration uint64      `json:"roll_generation"`
	Meter          float64     `json:"meter"`
	OverlapMM      float64     `json:"overlap_mm"`
	PullForceN     float64     `json:"pull_force_n"`
	Verdict        SeamVerdict `json:"verdict"`
	Reasons        []string    `json:"reasons"`
	RejectRange    MeterRange  `json:"reject_range"`
	InspectedAt    time.Time   `json:"inspected_at"`
	Supersedes     EventID     `json:"supersedes"`
}

func (certificate SeamCertificate) Validate() error {
	if certificate.ID == "" || certificate.SeamID == "" || certificate.Revision < 1 {
		return errors.New("seam certificate identity is incomplete")
	}
	if !certificate.JobID.Valid() || certificate.JobGeneration == 0 || certificate.RollGeneration == 0 {
		return errors.New("seam certificate fence is incomplete")
	}
	if certificate.Meter < 0 || certificate.OverlapMM <= 0 || certificate.PullForceN < 0 {
		return fmt.Errorf("seam certificate measurements are invalid")
	}
	if certificate.Verdict != SeamAccepted && certificate.Verdict != SeamRejected {
		return fmt.Errorf("seam certificate verdict %q is invalid", certificate.Verdict)
	}
	if certificate.Verdict == SeamRejected && !certificate.RejectRange.Valid() {
		return errors.New("rejected seam certificate requires a reject range")
	}
	if certificate.Verdict == SeamAccepted && certificate.RejectRange.Valid() {
		return errors.New("accepted seam certificate must not carry a reject range")
	}
	if certificate.InspectedAt.IsZero() {
		return errors.New("seam certificate inspection time is required")
	}
	return nil
}
