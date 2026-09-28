package splice

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

// 检验结论。复检只能新增一轮结论，旧结论原样保留在台账历史里。
const (
	VerdictAccepted = "accepted" // 接缝合格，其影响米段为合格区
	VerdictRejected = "rejected" // 接缝不合格，影响米段必须剔除
)

// Limits 是接缝检验的判定门限。搭接宽度低于下限或拉力低于下限即判不合格；
// 接缝两侧各剔除 RejectMargin 米，覆盖搭接区与热影响/起皱区。
type Limits struct {
	MinOverlapMM float64 `json:"min_overlap_mm"`
	MinPullN     float64 `json:"min_pull_n"`
	RejectMargin float64 `json:"reject_margin"`
}

// Inspection 是一张接缝检验证：记录接缝米位、搭接宽度和拉力实测，
// 以及据此判定出的合格区/必须剔除纸段。
type Inspection struct {
	ID             model.OperationID  `json:"id"`
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	Operation      model.OperationID  `json:"operation"`
	Revision       int                `json:"revision"` // 1=首检，2+ = 复检
	SeamMeter      float64            `json:"seam_meter"`
	OverlapMM      float64            `json:"overlap_mm"`
	PullN          float64            `json:"pull_n"`
	Limits         Limits             `json:"limits"`
	Verdict        string             `json:"verdict"`
	AcceptedRange  model.MeterRange   `json:"accepted_range"`
	RejectedRanges []model.MeterRange `json:"rejected_ranges"`
	InspectedAt    time.Time          `json:"inspected_at"`
}

// InspectionInput 是现场录入的检验实测值。
type InspectionInput struct {
	SeamMeter float64
	OverlapMM float64
	PullN     float64
	Limits    Limits
}

func (limits Limits) valid() bool {
	return limits.MinOverlapMM > 0 && limits.MinPullN > 0 && limits.RejectMargin >= 0
}

func (input InspectionInput) validate() error {
	if input.SeamMeter < 0 {
		return fmt.Errorf("seam meter cannot be negative")
	}
	if input.OverlapMM <= 0 {
		return fmt.Errorf("measured overlap width is required")
	}
	if input.PullN <= 0 {
		return fmt.Errorf("measured pull force is required")
	}
	if !input.Limits.valid() {
		return fmt.Errorf("splice inspection limits are incomplete")
	}
	return nil
}

// BuildInspection 依据实测值出具检验证。
// 合格：合格区是以接缝为中心、两侧各取 RejectMargin 的米段（搭接完好区），无剔除段；
// 不合格：合格区为空，必须剔除同一米段。两侧边界裁到非负。
func BuildInspection(job model.Job, operation model.OperationID, revision int, input InspectionInput, now time.Time) (Inspection, error) {
	if err := job.Validate(); err != nil {
		return Inspection{}, err
	}
	if !operation.Valid() {
		return Inspection{}, fmt.Errorf("splice inspection operation is required")
	}
	if revision < 1 {
		return Inspection{}, fmt.Errorf("splice inspection revision must start at 1")
	}
	if err := input.validate(); err != nil {
		return Inspection{}, err
	}
	seam := input.SeamMeter
	span := model.MeterRange{Start: max(0, seam-input.Limits.RejectMargin), End: seam + input.Limits.RejectMargin}
	inspection := Inspection{
		ID:             model.NewOperationID(),
		JobID:          job.ID,
		JobGeneration:  job.Generation,
		RollGeneration: job.RollGeneration,
		Operation:      operation,
		Revision:       revision,
		SeamMeter:      seam,
		OverlapMM:      input.OverlapMM,
		PullN:          input.PullN,
		Limits:         input.Limits,
		InspectedAt:    now.UTC(),
	}
	if input.OverlapMM >= input.Limits.MinOverlapMM && input.PullN >= input.Limits.MinPullN {
		inspection.Verdict = VerdictAccepted
		inspection.AcceptedRange = span
		inspection.RejectedRanges = nil
	} else {
		inspection.Verdict = VerdictRejected
		inspection.RejectedRanges = []model.MeterRange{span}
	}
	return inspection, nil
}

// BelongsTo 判断检验证是否属于指定的作业/纸卷代际。
// 跨代际（换卷后）的旧结论不作用于当前纸路。
func (inspection Inspection) BelongsTo(job model.Job) bool {
	return inspection.JobID == job.ID && inspection.JobGeneration == job.Generation && inspection.RollGeneration == job.RollGeneration
}

// Passed 返回该轮检验是否合格。
func (inspection Inspection) Passed() bool {
	return inspection.Verdict == VerdictAccepted
}

// Conclusion 是沿纸路传给成品段的检验结论：
// 当前代际下所有检验轮次并集后的剔除米段，以及最近一轮合格状态。
type Conclusion struct {
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	RejectedRanges []model.MeterRange `json:"rejected_ranges"`
	LatestVerdict  string             `json:"latest_verdict"`
	Revision       int                `json:"revision"`
}

// RejectedAt 返回给定米位是否落在必须剔除的纸段内。
func (conclusion Conclusion) RejectedAt(meter float64) bool {
	for _, meterRange := range conclusion.RejectedRanges {
		if meterRange.Contains(meter) {
			return true
		}
	}
	return false
}
