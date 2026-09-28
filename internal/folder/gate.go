package folder

import (
	"fmt"
	"sync"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

// RejectedLot 记录一次被成品段闸门拦下的纸批。
type RejectedLot struct {
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	LotRange       model.MeterRange   `json:"lot_range"`
	Overlaps       []model.MeterRange `json:"overlaps"`
	Revision       int                `json:"revision"`
	At             time.Time          `json:"at"`
}

// Gate 是成品段的接缝闸门：检验结论沿纸路送达后，
// 任何与必须剔除米段相交的成品纸批都不得放行。
//
// 同一代际内复检只追加结论：闸门把新旧剔除段取并集，
// 已剔除的段不会被后续复检（即使改判合格）覆盖或清除。
// 换卷（roll generation 变化）后旧代际剔除段自然失效。
type Gate struct {
	mu             sync.RWMutex
	jobID          model.JobID
	jobGeneration  uint64
	rollGeneration uint64
	revision       int
	rejected       []model.MeterRange
	rejections     []RejectedLot
}

func NewGate() *Gate {
	return &Gate{}
}

// Receive 接收沿纸路传来的一道检验结论。同代际复检时剔除段取并集，单调只增。
func (gate *Gate) Receive(jobID model.JobID, jobGeneration, rollGeneration uint64, ranges []model.MeterRange, revision int) error {
	if !jobID.Valid() || jobGeneration == 0 || rollGeneration == 0 {
		return fmt.Errorf("splice gate notice identity is incomplete")
	}
	if revision < 1 {
		return fmt.Errorf("splice gate notice revision is invalid")
	}
	for _, meterRange := range ranges {
		if !meterRange.Valid() {
			return fmt.Errorf("splice gate received an invalid rejected range")
		}
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.rollGeneration != 0 && (gate.jobID != jobID || gate.jobGeneration != jobGeneration || gate.rollGeneration != rollGeneration) {
		// 新作业/新纸卷代际：旧代际的剔除段不再作用于当前纸路。
		gate.rejected = nil
	}
	gate.jobID = jobID
	gate.jobGeneration = jobGeneration
	gate.rollGeneration = rollGeneration
	// 并集累积：复检不能缩小或清除任何已剔除段。
	combined := append([]model.MeterRange(nil), gate.rejected...)
	combined = append(combined, ranges...)
	gate.rejected = model.MergeMeterRanges(combined)
	if revision > gate.revision {
		gate.revision = revision
	}
	return nil
}

// Admit 校验成品纸批米段是否可以放行；与剔除段相交则拒绝并留痕。
func (gate *Gate) Admit(job model.Job, lot model.MeterRange, now time.Time) error {
	if err := job.Validate(); err != nil {
		return err
	}
	if !lot.Valid() || lot.Start == lot.End {
		return fmt.Errorf("finished lot meter range is invalid")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.jobID != job.ID || gate.jobGeneration != job.Generation || gate.rollGeneration != job.RollGeneration {
		// 当前代际没有任何接缝检验结论送达，无剔除约束。
		return nil
	}
	overlaps := []model.MeterRange{}
	for _, rejected := range gate.rejected {
		if rejected.Overlaps(lot) {
			overlaps = append(overlaps, rejected)
		}
	}
	if len(overlaps) == 0 {
		return nil
	}
	gate.rejections = append(gate.rejections, RejectedLot{
		JobID:          job.ID,
		JobGeneration:  job.Generation,
		RollGeneration: job.RollGeneration,
		LotRange:       lot,
		Overlaps:       overlaps,
		Revision:       gate.revision,
		At:             now.UTC(),
	})
	return fmt.Errorf("finished lot %.2f-%.2fm overlaps %d rejected splice segment(s)", lot.Start, lot.End, len(overlaps))
}

// Snapshot 返回闸门当前的剔除段与拦批记录。
func (gate *Gate) Snapshot() (model.JobID, uint64, uint64, []model.MeterRange, []RejectedLot) {
	gate.mu.RLock()
	defer gate.mu.RUnlock()
	rejected := append([]model.MeterRange(nil), gate.rejected...)
	rejections := append([]RejectedLot(nil), gate.rejections...)
	if rejected == nil {
		rejected = []model.MeterRange{}
	}
	if rejections == nil {
		rejections = []RejectedLot{}
	}
	return gate.jobID, gate.jobGeneration, gate.rollGeneration, rejected, rejections
}
