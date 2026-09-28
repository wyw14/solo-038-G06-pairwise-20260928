package service

import (
	"fmt"
	"sort"
	"time"

	"github.com/wyw14/solo-038/internal/folder"
	"github.com/wyw14/solo-038/internal/journal"
	"github.com/wyw14/solo-038/internal/model"
	"github.com/wyw14/solo-038/internal/splice"
)

// Fixed seam identity and lot geometry used by the canned operations. A roll
// change re-inspects the same splice position, which is exactly the
// reinspection case the append-only ledger must protect.
const (
	demoSeamID    = "splice-seam-1"
	demoSeamMeter = 120.0
	sheetsPerLot  = 500
	cutLengthM    = 0.630
)

type SeamInspectionResult struct {
	Operation   model.OperationID     `json:"operation"`
	Certificate model.SeamCertificate `json:"certificate"`
	Revision    int                   `json:"revision"`
	Reinspected bool                  `json:"reinspected"`
}

func (runtime *Runtime) inspectSeam(current model.Job, measurement splice.SeamMeasurement, now time.Time) (SeamInspectionResult, error) {
	certificate, err := splice.Inspect(measurement, now)
	if err != nil {
		return SeamInspectionResult{}, err
	}
	certificate, err = runtime.seamLedger.Record(certificate)
	if err != nil {
		return SeamInspectionResult{}, err
	}
	operation := model.NewOperationID()
	if _, err := journal.PersistSeamInspection(runtime.journal, current, operation, journal.SeamRecord{Certificate: certificate, Released: true}, now); err != nil {
		return SeamInspectionResult{}, err
	}
	if err := runtime.seamRouter.Enter(certificate); err != nil {
		return SeamInspectionResult{}, err
	}
	if certificate.Verdict == model.SeamRejected {
		runtime.RecordIncident("error", "splice", fmt.Sprintf("seam %s rejected at meter %.1f: segment %v must be cut out", certificate.SeamID, certificate.Meter, certificate.RejectRange))
	}
	return SeamInspectionResult{Operation: operation, Certificate: certificate, Revision: certificate.Revision, Reinspected: certificate.Revision > 1}, nil
}

func (runtime *Runtime) demoMeasurement(current model.Job, overlapMM, pullForceN float64) splice.SeamMeasurement {
	return splice.SeamMeasurement{SeamID: demoSeamID, JobID: current.ID, JobGeneration: current.Generation, RollGeneration: current.RollGeneration, Meter: demoSeamMeter, OverlapMM: overlapMM, PullForceN: pullForceN}
}

// propagateSeam walks the certificate through press, dryer and folder. Only
// once every segment has been passed in order is the conclusion handed to the
// folder quality gate and the hand-off journaled.
func (runtime *Runtime) propagateSeam(current model.Job, certificate model.SeamCertificate, now time.Time) error {
	positions := map[string]float64{"press": certificate.Meter + 12, "dryer": certificate.Meter + 30, "folder": certificate.Meter + 35}
	for _, segment := range splice.ProductionRoute {
		if err := runtime.seamRouter.Pass(certificate.ID, segment, positions[segment]); err != nil {
			return err
		}
	}
	if !runtime.seamRouter.ArrivedAtFolder(certificate.ID) {
		return fmt.Errorf("seam certificate %s did not reach the folder", certificate.ID)
	}
	notice := folder.SeamNotice{
		JobID:          current.ID,
		JobGeneration:  current.Generation,
		RollGeneration: current.RollGeneration,
		CertificateID:  certificate.ID,
		SeamID:         certificate.SeamID,
		Revision:       certificate.Revision,
		Verdict:        certificate.Verdict,
		RejectRange:    certificate.RejectRange,
		RejectedRanges: runtime.seamLedger.RejectedRanges(current.ID, current.Generation, current.RollGeneration),
	}
	if err := runtime.seamGate.Receive(notice); err != nil {
		return err
	}
	if err := runtime.seamRouter.MarkDelivered(certificate.ID); err != nil {
		return err
	}
	operation := model.NewOperationID()
	_, err := journal.PersistSeamDelivery(runtime.journal, current, operation, journal.SeamDeliveryRecord{
		JobID:          current.ID,
		JobGeneration:  current.Generation,
		RollGeneration: current.RollGeneration,
		CertificateID:  notice.CertificateID,
		SeamID:         notice.SeamID,
		Revision:       notice.Revision,
		Verdict:        notice.Verdict,
		RejectRange:    notice.RejectRange,
		RejectedRanges: notice.RejectedRanges,
	}, now)
	return err
}

func (runtime *Runtime) inspectSpliceOperation(current model.Job) error {
	now := time.Now()
	// First inspection finds the tape overlap short and the pull test weak.
	result, err := runtime.inspectSeam(current, runtime.demoMeasurement(current, 35, 120), now)
	if err != nil {
		return err
	}
	return runtime.propagateSeam(current, result.Certificate, now)
}

func (runtime *Runtime) reinspectSpliceOperation(current model.Job) error {
	now := time.Now()
	if _, ok := runtime.seamLedger.Current(demoSeamID); !ok {
		return fmt.Errorf("seam %s has no prior inspection to revise", demoSeamID)
	}
	// Reinspection passes every criterion — but the earlier reject stands.
	result, err := runtime.inspectSeam(current, runtime.demoMeasurement(current, 50, 200), now)
	if err != nil {
		return err
	}
	if !result.Reinspected {
		return fmt.Errorf("seam reinspection did not append a revision")
	}
	return runtime.propagateSeam(current, result.Certificate, now)
}

// discardSeamSegmentsOperation records the physical cut-out of condemned paper.
func (runtime *Runtime) discardSeamSegmentsOperation(current model.Job) error {
	now := time.Now()
	rejected := runtime.seamLedger.RejectedRanges(current.ID, current.Generation, current.RollGeneration)
	if len(rejected) == 0 {
		return fmt.Errorf("there are no condemned segments to discard for the current fence")
	}
	cut, err := runtime.seamGate.Discard(current, rejected)
	if err != nil {
		return err
	}
	operation := model.NewOperationID()
	if _, err := journal.PersistSeamDiscard(runtime.journal, current, operation, cut, now); err != nil {
		return err
	}
	runtime.RecordIncident("info", "folder", fmt.Sprintf("condemned segments %v cut out before finishing", cut))
	return nil
}

// lotWindow is the meter span of the canned 500-sheet lot. It sits downstream
// of the demo splice position so a condemned seam blocks the first lot.
func lotWindow() model.MeterRange {
	return model.MeterRange{From: demoSeamMeter, To: demoSeamMeter + float64(sheetsPerLot)*cutLengthM}
}

// SeamReport is the splice-inspection view served to operators.
type SeamReport struct {
	JobID          model.JobID             `json:"job_id"`
	JobGeneration  uint64                  `json:"job_generation"`
	RollGeneration uint64                  `json:"roll_generation"`
	Certificates   []model.SeamCertificate `json:"certificates"`
	RejectedRanges []model.MeterRange      `json:"rejected_ranges"`
	Routes         []splice.SeamRoute      `json:"routes"`
	Delivered      []folder.SeamNotice     `json:"delivered"`
	FolderRejected []model.MeterRange      `json:"folder_rejected_ranges"`
	Discarded      []model.MeterRange      `json:"discarded_ranges"`
}

func (runtime *Runtime) SeamReport() SeamReport {
	empty := func() SeamReport {
		return SeamReport{Certificates: []model.SeamCertificate{}, RejectedRanges: []model.MeterRange{}, Routes: []splice.SeamRoute{}, Delivered: []folder.SeamNotice{}, FolderRejected: []model.MeterRange{}, Discarded: []model.MeterRange{}}
	}
	current, ok := runtime.jobs.Current()
	if !ok {
		return empty()
	}
	certificates := runtime.seamLedger.Certificates(current.ID, current.Generation, current.RollGeneration)
	routes := make([]splice.SeamRoute, 0, len(certificates))
	for _, certificate := range runtime.seamLedger.AllCertificates(current.ID, current.Generation, current.RollGeneration) {
		if route, ok := runtime.seamRouter.Route(certificate.ID); ok {
			routes = append(routes, route)
		}
	}
	sort.Slice(routes, func(left, right int) bool { return routes[left].CertificateID < routes[right].CertificateID })
	return SeamReport{
		JobID:          current.ID,
		JobGeneration:  current.Generation,
		RollGeneration: current.RollGeneration,
		Certificates:   certificates,
		RejectedRanges: runtime.seamLedger.RejectedRanges(current.ID, current.Generation, current.RollGeneration),
		Routes:         routes,
		Delivered:      runtime.seamGate.Notices(),
		FolderRejected: runtime.seamGate.RejectedRanges(current),
		Discarded:      runtime.seamGate.DiscardedRanges(current),
	}
}

// RestoreSeamEvidence rebuilds the ledger, router and folder gate from journal
// events. Old conclusions — including every previously condemned segment —
// are replayed verbatim and never overwritten.
func (runtime *Runtime) RestoreSeamEvidence(recovered journal.RecoveredState) error {
	runtime.seamGate.Reset()
	runtime.seamRouter.Reset()
	runtime.seamLedger.Reset()
	if err := runtime.seamLedger.Restore(recovered.Seams); err != nil {
		return err
	}
	delivered := map[model.EventID]bool{}
	for _, delivery := range recovered.SeamDeliveries {
		notice := folder.SeamNotice{
			JobID:          delivery.JobID,
			JobGeneration:  delivery.JobGeneration,
			RollGeneration: delivery.RollGeneration,
			CertificateID:  delivery.CertificateID,
			SeamID:         delivery.SeamID,
			Revision:       delivery.Revision,
			Verdict:        delivery.Verdict,
			RejectRange:    delivery.RejectRange,
			RejectedRanges: delivery.RejectedRanges,
		}
		if err := runtime.seamGate.Receive(notice); err != nil {
			return err
		}
		delivered[delivery.CertificateID] = true
	}
	for _, discard := range recovered.SeamDiscards {
		job := model.Job{ID: discard.JobID, State: model.JobComplete, Generation: discard.JobGeneration, RollGeneration: discard.RollGeneration}
		runtime.seamGate.RestoreDiscarded(job, discard.Segments)
	}
	for _, certificate := range recovered.Seams {
		// Every inspected conclusion re-enters the web path; those already
		// delivered before the restart are advanced straight to the folder.
		if err := runtime.seamRouter.Enter(certificate); err != nil {
			return err
		}
		if delivered[certificate.ID] {
			for _, segment := range splice.ProductionRoute {
				if err := runtime.seamRouter.Pass(certificate.ID, segment, certificate.Meter); err != nil {
					return err
				}
			}
			if err := runtime.seamRouter.MarkDelivered(certificate.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
