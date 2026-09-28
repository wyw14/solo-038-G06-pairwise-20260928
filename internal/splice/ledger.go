package splice

import (
	"sort"
	"sync"

	"github.com/wyw14/solo-038/internal/model"
)

// InspectionLedger is the append-only book of seam certificates. Every
// inspection — first pass or reinspection — is appended as a new revision;
// previous conclusions are kept verbatim and the effective reject set is the
// union of every rejected record, so a reinspection can never shrink or
// overwrite a segment that was already condemned.
type InspectionLedger struct {
	mu      sync.RWMutex
	bySeam  map[string][]model.SeamCertificate
	current map[string]model.SeamCertificate
}

func NewInspectionLedger() *InspectionLedger {
	return &InspectionLedger{bySeam: map[string][]model.SeamCertificate{}, current: map[string]model.SeamCertificate{}}
}

// Reset clears the book before rebuilding it from the journal, so recovery can be replayed.
func (ledger *InspectionLedger) Reset() {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.bySeam = map[string][]model.SeamCertificate{}
	ledger.current = map[string]model.SeamCertificate{}
}

// Record appends a certificate for its seam. The incoming revision/supersedes
// fields are assigned here so callers cannot forge the chain.
func (ledger *InspectionLedger) Record(certificate model.SeamCertificate) (model.SeamCertificate, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	history := ledger.bySeam[certificate.SeamID]
	certificate.Revision = len(history) + 1
	certificate.Supersedes = ""
	if len(history) > 0 {
		certificate.Supersedes = history[len(history)-1].ID
	}
	if err := certificate.Validate(); err != nil {
		return model.SeamCertificate{}, err
	}
	ledger.bySeam[certificate.SeamID] = append(history, certificate)
	ledger.current[certificate.SeamID] = certificate
	return certificate, nil
}

// Restore replays persisted certificates (journal recovery). Records are kept
// exactly as stored — revisions are not renumbered and reject ranges are
// unioned, never dropped.
func (ledger *InspectionLedger) Restore(certificates []model.SeamCertificate) error {
	ordered := append([]model.SeamCertificate(nil), certificates...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].InspectedAt.Equal(ordered[right].InspectedAt) {
			return ordered[left].Revision < ordered[right].Revision
		}
		return ordered[left].InspectedAt.Before(ordered[right].InspectedAt)
	})
	for _, certificate := range ordered {
		if err := certificate.Validate(); err != nil {
			return err
		}
		ledger.mu.Lock()
		ledger.bySeam[certificate.SeamID] = append(ledger.bySeam[certificate.SeamID], certificate)
		ledger.current[certificate.SeamID] = certificate
		ledger.mu.Unlock()
	}
	return nil
}

// History returns every revision recorded for one seam, oldest first.
func (ledger *InspectionLedger) History(seamID string) []model.SeamCertificate {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	return append([]model.SeamCertificate(nil), ledger.bySeam[seamID]...)
}

// Current returns the latest revision for one seam.
func (ledger *InspectionLedger) Current(seamID string) (model.SeamCertificate, bool) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	certificate, ok := ledger.current[seamID]
	return certificate, ok
}

// Certificates returns the latest revision of every seam of the job fence.
func (ledger *InspectionLedger) Certificates(jobID model.JobID, jobGeneration, rollGeneration uint64) []model.SeamCertificate {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	values := make([]model.SeamCertificate, 0, len(ledger.current))
	for _, certificate := range ledger.current {
		if certificate.JobID == jobID && certificate.JobGeneration == jobGeneration && certificate.RollGeneration == rollGeneration {
			values = append(values, certificate)
		}
	}
	sort.Slice(values, func(left, right int) bool { return values[left].Meter < values[right].Meter })
	return values
}

// AllCertificates returns every revision of every seam of the job fence,
// oldest revision first — the full inspection trail, not just current verdicts.
func (ledger *InspectionLedger) AllCertificates(jobID model.JobID, jobGeneration, rollGeneration uint64) []model.SeamCertificate {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	values := []model.SeamCertificate{}
	for _, history := range ledger.bySeam {
		for _, certificate := range history {
			if certificate.JobID == jobID && certificate.JobGeneration == jobGeneration && certificate.RollGeneration == rollGeneration {
				values = append(values, certificate)
			}
		}
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].Meter == values[right].Meter {
			return values[left].Revision < values[right].Revision
		}
		return values[left].Meter < values[right].Meter
	})
	return values
}

// RejectedRanges is the union of reject ranges across ALL revisions of the job
// fence — including revisions later superseded by an accepted reinspection.
func (ledger *InspectionLedger) RejectedRanges(jobID model.JobID, jobGeneration, rollGeneration uint64) []model.MeterRange {
	ranges := []model.MeterRange{}
	ledger.mu.RLock()
	for _, history := range ledger.bySeam {
		for _, certificate := range history {
			if certificate.JobID == jobID && certificate.JobGeneration == jobGeneration && certificate.RollGeneration == rollGeneration && certificate.Verdict == model.SeamRejected {
				ranges = append(ranges, certificate.RejectRange)
			}
		}
	}
	ledger.mu.RUnlock()
	return model.MergeMeterRanges(ranges)
}

// RejectedAt reports whether the meter position falls inside any condemned segment.
func (ledger *InspectionLedger) RejectedAt(jobID model.JobID, jobGeneration, rollGeneration uint64, meter float64) bool {
	for _, meterRange := range ledger.RejectedRanges(jobID, jobGeneration, rollGeneration) {
		if meter >= meterRange.From && meter < meterRange.To {
			return true
		}
	}
	return false
}

// LotWindow classifies a finished-lot meter window against the ledger: the
// accepted remainder after the condemned union is subtracted.
func (ledger *InspectionLedger) LotWindow(jobID model.JobID, jobGeneration, rollGeneration uint64, window model.MeterRange) (accepted []model.MeterRange, rejected []model.MeterRange) {
	rejected = ledger.RejectedRanges(jobID, jobGeneration, rollGeneration)
	if !window.Valid() {
		return nil, rejected
	}
	return model.SubtractMeterRanges([]model.MeterRange{window}, rejected), rejected
}
