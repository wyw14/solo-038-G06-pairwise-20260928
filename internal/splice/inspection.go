package splice

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

// Acceptance criteria for a splice seam. The overlap must land inside the tape
// window and the measured pull force must hold the minimum break load.
const (
	MinOverlapMM       = 40.0
	MaxOverlapMM       = 60.0
	MinPullForceN      = 150.0
	RejectMarginMeters = 5.0
)

// SeamMeasurement carries the values recorded at the splice table for one seam.
type SeamMeasurement struct {
	SeamID         string
	JobID          model.JobID
	JobGeneration  uint64
	RollGeneration uint64
	Meter          float64
	OverlapMM      float64
	PullForceN     float64
}

func (measurement SeamMeasurement) Validate() error {
	if measurement.SeamID == "" {
		return fmt.Errorf("seam id is required")
	}
	if !measurement.JobID.Valid() || measurement.JobGeneration == 0 || measurement.RollGeneration == 0 {
		return fmt.Errorf("seam measurement fence is incomplete")
	}
	if measurement.Meter < 0 {
		return fmt.Errorf("seam meter is invalid")
	}
	if measurement.OverlapMM <= 0 {
		return fmt.Errorf("seam overlap measurement is required")
	}
	if measurement.PullForceN < 0 {
		return fmt.Errorf("seam pull force measurement is invalid")
	}
	return nil
}

// RejectSpan returns the web segment that must be cut out around a failed seam:
// the measured position plus the safety margin on both sides.
func RejectSpan(measurement SeamMeasurement) model.MeterRange {
	from := measurement.Meter - RejectMarginMeters
	if from < 0 {
		from = 0
	}
	return model.MeterRange{From: from, To: measurement.Meter + RejectMarginMeters}
}

// Inspect evaluates one measurement against the acceptance criteria and builds
// the certificate. Revision and Supersedes stay zero here; the ledger assigns
// them when the record is appended.
func Inspect(measurement SeamMeasurement, now time.Time) (model.SeamCertificate, error) {
	if err := measurement.Validate(); err != nil {
		return model.SeamCertificate{}, err
	}
	reasons := []string{}
	if measurement.OverlapMM < MinOverlapMM || measurement.OverlapMM > MaxOverlapMM {
		reasons = append(reasons, fmt.Sprintf("overlap %.1fmm outside %.0f-%.0fmm", measurement.OverlapMM, MinOverlapMM, MaxOverlapMM))
	}
	if measurement.PullForceN < MinPullForceN {
		reasons = append(reasons, fmt.Sprintf("pull force %.1fN below %.0fN", measurement.PullForceN, MinPullForceN))
	}
	certificate := model.SeamCertificate{
		ID:             model.NewEventID(),
		SeamID:         measurement.SeamID,
		Revision:       1,
		JobID:          measurement.JobID,
		JobGeneration:  measurement.JobGeneration,
		RollGeneration: measurement.RollGeneration,
		Meter:          measurement.Meter,
		OverlapMM:      measurement.OverlapMM,
		PullForceN:     measurement.PullForceN,
		InspectedAt:    now.UTC(),
	}
	if len(reasons) > 0 {
		certificate.Verdict = model.SeamRejected
		certificate.Reasons = reasons
		certificate.RejectRange = RejectSpan(measurement)
	} else {
		certificate.Verdict = model.SeamAccepted
		certificate.Reasons = []string{}
	}
	return certificate, certificate.Validate()
}
