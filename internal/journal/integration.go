package journal

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

type IntervalRecord struct {
	Interval model.Interval `json:"interval"`
	Proofs   []model.Proof  `json:"proofs"`
	Released bool           `json:"released"`
}

// SeamRecord is the durable form of one seam inspection certificate.
type SeamRecord struct {
	Certificate model.SeamCertificate `json:"certificate"`
	Released    bool                  `json:"released"`
}

// SeamDeliveryRecord is the durable form of a conclusion handed to the folder.
type SeamDeliveryRecord struct {
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	CertificateID  model.EventID      `json:"certificate_id"`
	SeamID         string             `json:"seam_id"`
	Revision       int                `json:"revision"`
	Verdict        model.SeamVerdict  `json:"verdict"`
	RejectRange    model.MeterRange   `json:"reject_range"`
	RejectedRanges []model.MeterRange `json:"rejected_ranges"`
}

// SeamDiscardRecord is the durable confirmation that condemned paper was cut out.
type SeamDiscardRecord struct {
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	Segments       []model.MeterRange `json:"segments"`
}

func PersistSeamInspection(store *Store, job model.Job, operation model.OperationID, record SeamRecord, now time.Time) (uint64, error) {
	if err := record.Certificate.Validate(); err != nil {
		return 0, err
	}
	event, err := model.NewEvent(operation, job, "splice_seam_inspected", record, now)
	if err != nil {
		return 0, err
	}
	sequence, err := store.Append(event)
	if err != nil {
		return sequence, err
	}
	if !store.State().IsDurable(sequence) {
		return sequence, fmt.Errorf("seam inspection sequence %d is not durable", sequence)
	}
	return sequence, nil
}

func PersistSeamDelivery(store *Store, job model.Job, operation model.OperationID, record SeamDeliveryRecord, now time.Time) (uint64, error) {
	if record.CertificateID == "" || record.SeamID == "" {
		return 0, fmt.Errorf("seam delivery identity is incomplete")
	}
	event, err := model.NewEvent(operation, job, "splice_seam_delivered", record, now)
	if err != nil {
		return 0, err
	}
	sequence, err := store.Append(event)
	if err != nil {
		return sequence, err
	}
	if !store.State().IsDurable(sequence) {
		return sequence, fmt.Errorf("seam delivery sequence %d is not durable", sequence)
	}
	return sequence, nil
}

func PersistSeamDiscard(store *Store, job model.Job, operation model.OperationID, segments []model.MeterRange, now time.Time) (uint64, error) {
	if len(model.MergeMeterRanges(segments)) == 0 {
		return 0, fmt.Errorf("seam discard requires at least one segment")
	}
	record := SeamDiscardRecord{JobID: job.ID, JobGeneration: job.Generation, RollGeneration: job.RollGeneration, Segments: model.MergeMeterRanges(segments)}
	event, err := model.NewEvent(operation, job, "splice_seam_discarded", record, now)
	if err != nil {
		return 0, err
	}
	sequence, err := store.Append(event)
	if err != nil {
		return sequence, err
	}
	if !store.State().IsDurable(sequence) {
		return sequence, fmt.Errorf("seam discard sequence %d is not durable", sequence)
	}
	return sequence, nil
}

func PersistInterval(store *Store, job model.Job, operation model.OperationID, record IntervalRecord, now time.Time) (uint64, error) {
	if err := record.Interval.Validate(); err != nil {
		return 0, err
	}
	event, err := model.NewEvent(operation, job, "interval_committed", record, now)
	if err != nil {
		return 0, err
	}
	sequence, err := store.Append(event)
	if err != nil {
		return sequence, err
	}
	if !store.State().IsDurable(sequence) {
		return sequence, fmt.Errorf("interval sequence %d is not durable", sequence)
	}
	return sequence, nil
}

func PersistTransition(store *Store, job model.Job, operation model.OperationID, from, to model.JobState, now time.Time) (uint64, error) {
	payload := map[string]any{"from": from, "to": to, "generation": job.Generation}
	event, err := model.NewEvent(operation, job, "job_transitioned", payload, now)
	if err != nil {
		return 0, err
	}
	return store.Append(event)
}

func PersistFence(store *Store, job model.Job, operation model.OperationID, owner string, now time.Time) (uint64, error) {
	payload := map[string]any{"owner": owner, "roll_generation": job.RollGeneration}
	event, err := model.NewEvent(operation, job, "web_fence_restored", payload, now)
	if err != nil {
		return 0, err
	}
	return store.Append(event)
}

func PersistJobSnapshot(store *Store, job model.Job, operation model.OperationID, now time.Time) (uint64, error) {
	if err := job.Validate(); err != nil {
		return 0, err
	}
	event, err := model.NewEvent(operation, job, "job_snapshot", job, now)
	if err != nil {
		return 0, err
	}
	return store.Append(event)
}
