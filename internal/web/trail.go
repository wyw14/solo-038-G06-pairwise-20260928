package web

import (
	"fmt"
	"sync"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

// TrailNotice 是沿纸路向下游（成品段）传递的一道接缝检验结论。
type TrailNotice struct {
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	RejectedRanges []model.MeterRange `json:"rejected_ranges"`
	LatestVerdict  string             `json:"latest_verdict"`
	Revision       int                `json:"revision"`
	SeamCount      int                `json:"seam_count"`
	ForwardedAt    time.Time          `json:"forwarded_at"`
}

type trailKey struct {
	job            model.JobID
	jobGeneration  uint64
	rollGeneration uint64
}

// PaperTrail 沿纸路携带各代际的接缝检验结论。
//
// 它只追加公告、从不改写旧公告：复检产生一道 revision 更高的新公告，
// 而必须剔除的纸段在发布时已是历轮并集，因此已剔除段随公告单调保留。
// 换卷（新 roll generation）后，旧代际公告不再作用于当前纸路，但仍留在轨迹里可追溯。
type PaperTrail struct {
	mu      sync.RWMutex
	notices []TrailNotice
	lastRev map[trailKey]int
}

func NewPaperTrail() *PaperTrail {
	return &PaperTrail{lastRev: map[trailKey]int{}}
}

// Publish 沿纸路发布一道检验结论。ranges 会先合并成并集，剔除段只增不减。
// 同一代际内只接受更高 revision 的复检公告；旧/重复轮次幂等忽略（用于恢复重放）。
func (trail *PaperTrail) Publish(notice TrailNotice) error {
	if !notice.JobID.Valid() || notice.JobGeneration == 0 || notice.RollGeneration == 0 {
		return fmt.Errorf("paper trail notice identity is incomplete")
	}
	if notice.Revision < 1 {
		return fmt.Errorf("paper trail notice revision is invalid")
	}
	if notice.LatestVerdict == "" {
		return fmt.Errorf("paper trail notice verdict is required")
	}
	for _, meterRange := range notice.RejectedRanges {
		if !meterRange.Valid() {
			return fmt.Errorf("paper trail notice carries an invalid rejected range")
		}
	}
	notice.RejectedRanges = model.MergeMeterRanges(notice.RejectedRanges)
	notice.ForwardedAt = notice.ForwardedAt.UTC()
	key := trailKey{job: notice.JobID, jobGeneration: notice.JobGeneration, rollGeneration: notice.RollGeneration}
	trail.mu.Lock()
	defer trail.mu.Unlock()
	if notice.Revision <= trail.lastRev[key] {
		return nil // 该轮次结论已沿纸路发布，幂等丢弃。
	}
	trail.lastRev[key] = notice.Revision
	trail.notices = append(trail.notices, notice)
	return nil
}

// Current 返回当前代际沿纸路最新的一道公告（复检公告覆盖为“当前结论”，
// 但其 RejectedRanges 已含并集，旧剔除段不会丢失）；无公告时返回 false。
func (trail *PaperTrail) Current(jobID model.JobID, jobGeneration, rollGeneration uint64) (TrailNotice, bool) {
	trail.mu.RLock()
	defer trail.mu.RUnlock()
	for index := len(trail.notices) - 1; index >= 0; index-- {
		notice := trail.notices[index]
		if notice.JobID == jobID && notice.JobGeneration == jobGeneration && notice.RollGeneration == rollGeneration {
			return notice, true
		}
	}
	return TrailNotice{}, false
}

// RejectedRanges 返回当前代际最新公告携带的、必须剔除的米段并集。
func (trail *PaperTrail) RejectedRanges(jobID model.JobID, jobGeneration, rollGeneration uint64) []model.MeterRange {
	notice, ok := trail.Current(jobID, jobGeneration, rollGeneration)
	if !ok {
		return nil
	}
	return append([]model.MeterRange(nil), notice.RejectedRanges...)
}

// Notices 返回全部历史公告（按发布顺序），旧结论原样保留，供复检追溯与快照。
func (trail *PaperTrail) Notices() []TrailNotice {
	trail.mu.RLock()
	defer trail.mu.RUnlock()
	return append([]TrailNotice(nil), trail.notices...)
}
