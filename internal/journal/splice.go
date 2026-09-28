package journal

import (
	"fmt"
	"sort"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

// SeamInspectionRecord 是接缝检验证在事件流里的载荷。
// 放在 journal 侧（而非直接引用 splice.Inspection），避免 journal 与 splplice 之间形成导入环。
type SeamInspectionRecord struct {
	InspectionID   model.OperationID  `json:"inspection_id"`
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	Operation      model.OperationID  `json:"operation"`
	Revision       int                `json:"revision"`
	SeamMeter      float64            `json:"seam_meter"`
	OverlapMM      float64            `json:"overlap_mm"`
	PullN          float64            `json:"pull_n"`
	Limits         SeamLimitsRecord   `json:"limits"`
	Verdict        string             `json:"verdict"`
	AcceptedRange  model.MeterRange   `json:"accepted_range"`
	RejectedRanges []model.MeterRange `json:"rejected_ranges"`
	InspectedAt    time.Time          `json:"inspected_at"`
}

// SeamLimitsRecord 是接缝判定门限的事件载荷。
type SeamLimitsRecord struct {
	MinOverlapMM float64 `json:"min_overlap_mm"`
	MinPullN     float64 `json:"min_pull_n"`
	RejectMargin float64 `json:"reject_margin"`
}

func PersistSeamInspection(store *Store, job model.Job, record SeamInspectionRecord, now time.Time) (uint64, error) {
	if !job.ID.Valid() {
		return 0, fmt.Errorf("seam inspection job is required")
	}
	if record.JobID != job.ID {
		return 0, fmt.Errorf("seam inspection record does not belong to job %s", job.ID)
	}
	if !record.InspectionID.Valid() || !record.Operation.Valid() {
		return 0, fmt.Errorf("seam inspection identity is incomplete")
	}
	if record.Revision < 1 || record.SeamMeter < 0 || record.OverlapMM <= 0 || record.PullN <= 0 {
		return 0, fmt.Errorf("seam inspection measurements are incomplete")
	}
	if record.Verdict != "accepted" && record.Verdict != "rejected" {
		return 0, fmt.Errorf("seam inspection verdict %q is invalid", record.Verdict)
	}
	if record.Limits.MinOverlapMM <= 0 || record.Limits.MinPullN <= 0 || record.Limits.RejectMargin < 0 {
		return 0, fmt.Errorf("seam inspection limits are incomplete")
	}
	for _, meterRange := range record.RejectedRanges {
		if !meterRange.Valid() {
			return 0, fmt.Errorf("seam inspection rejected range is invalid")
		}
	}
	event, err := model.NewEvent(record.Operation, job, "splice_seam_inspected", record, now)
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

// ReplaySeamInspections 从事件流中按时间顺序提取接缝检验记录。
// 与 Replay 不同，它不要求存在作业快照，便于单独重建接缝台账。
func ReplaySeamInspections(events []model.Event) ([]SeamInspectionRecord, error) {
	ordered := append([]model.Event(nil), events...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].At.Before(ordered[right].At)
	})
	records := make([]SeamInspectionRecord, 0)
	for _, event := range ordered {
		if event.Kind != "splice_seam_inspected" {
			continue
		}
		if err := event.Validate(); err != nil {
			return nil, err
		}
		record, err := model.DecodeEventPayload[SeamInspectionRecord](event)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
