package service

import (
	"context"
	"fmt"
	"time"

	"github.com/wyw14/solo-038/internal/model"
	"github.com/wyw14/solo-038/internal/splice"
	"github.com/wyw14/solo-038/internal/web"
)

// 默认接缝检验门限：搭接宽度下限 40mm、接缝拉力下限 60N、接缝两侧各剔除 2m。
var defaultSpliceLimits = splice.Limits{MinOverlapMM: 40, MinPullN: 60, RejectMargin: 2}

// SeamInspectionRequest 是接缝检验证的录入实测值。
type SeamInspectionRequest struct {
	SeamMeter float64 `json:"seam_meter"`
	OverlapMM float64 `json:"overlap_mm"`
	PullN     float64 `json:"pull_n"`
}

// SeamInspectionView 是回给操作员/下游的检验证与沿纸路结论。
type SeamInspectionView struct {
	Inspection splice.Inspection   `json:"inspection"`
	History    []splice.Inspection `json:"history"`
	Conclusion splice.Conclusion   `json:"conclusion"`
	Notice     web.TrailNotice     `json:"notice"`
}

func (request SeamInspectionRequest) validate() error {
	if request.SeamMeter < 0 {
		return fmt.Errorf("seam meter cannot be negative")
	}
	if request.OverlapMM <= 0 {
		return fmt.Errorf("measured overlap width is required")
	}
	if request.PullN <= 0 {
		return fmt.Errorf("measured pull force is required")
	}
	return nil
}

// InspectSplice 出具接缝检验证：记录米位/搭接/拉力实测，判定合格区与必须剔除纸段，
// 再把结论沿纸路传给成品段。复检时旧检验证保留，已剔除纸段并集累积、不可被覆盖。
func (runtime *Runtime) InspectSplice(ctx context.Context, request SeamInspectionRequest) (SeamInspectionView, error) {
	if err := request.validate(); err != nil {
		return SeamInspectionView{}, err
	}
	current, ok := runtime.jobs.Current()
	if !ok {
		return SeamInspectionView{}, fmt.Errorf("there is no active job")
	}
	select {
	case <-ctx.Done():
		return SeamInspectionView{}, ctx.Err()
	default:
	}
	now := time.Now()
	operation := model.NewOperationID()
	input := splice.InspectionInput{SeamMeter: request.SeamMeter, OverlapMM: request.OverlapMM, PullN: request.PullN, Limits: defaultSpliceLimits}
	inspection, err := runtime.spliceInspections.Record(current, operation, input, now)
	if err != nil {
		return SeamInspectionView{}, err
	}
	notice, err := runtime.forwardSpliceConclusion(current, now)
	if err != nil {
		return SeamInspectionView{}, err
	}
	conclusion, _ := runtime.spliceInspections.Conclusion(current)
	view := SeamInspectionView{Inspection: inspection, History: runtime.spliceInspections.History(current), Conclusion: conclusion, Notice: notice}
	if !inspection.Passed() {
		runtime.RecordIncident("warning", "splice", fmt.Sprintf("splice at %.2fm rejected: overlap %.1fmm pull %.1fN", inspection.SeamMeter, inspection.OverlapMM, inspection.PullN))
	}
	return view, nil
}

// forwardSpliceConclusion 把当前代际的最新检验结论沿纸路发布并送达成品段闸门。
func (runtime *Runtime) forwardSpliceConclusion(current model.Job, now time.Time) (web.TrailNotice, error) {
	conclusion, ok := runtime.spliceInspections.Conclusion(current)
	if !ok {
		return web.TrailNotice{}, fmt.Errorf("splice inspection conclusion is missing")
	}
	notice := web.TrailNotice{JobID: conclusion.JobID, JobGeneration: conclusion.JobGeneration, RollGeneration: conclusion.RollGeneration, RejectedRanges: conclusion.RejectedRanges, LatestVerdict: conclusion.LatestVerdict, Revision: conclusion.Revision, SeamCount: len(runtime.spliceInspections.History(current)), ForwardedAt: now}
	if err := runtime.paperTrail.Publish(notice); err != nil {
		return web.TrailNotice{}, err
	}
	if err := runtime.spliceGate.Receive(conclusion.JobID, conclusion.JobGeneration, conclusion.RollGeneration, conclusion.RejectedRanges, conclusion.Revision); err != nil {
		return web.TrailNotice{}, err
	}
	return notice, nil
}

// SpliceInspectionStatus 返回当前作业的检验历史、汇总结论、纸路公告与成品闸门状态。
func (runtime *Runtime) SpliceInspectionStatus() map[string]any {
	current, ok := runtime.jobs.Current()
	status := map[string]any{"job_id": model.JobID(""), "history": []splice.Inspection{}, "conclusion": nil, "notices": runtime.paperTrail.Notices()}
	if !ok {
		return status
	}
	conclusion, hasConclusion := runtime.spliceInspections.Conclusion(current)
	gateJob, gateJobGen, gateRollGen, gateRejected, gateRejections := runtime.spliceGate.Snapshot()
	status["job_id"] = current.ID
	status["history"] = runtime.spliceInspections.History(current)
	if hasConclusion {
		status["conclusion"] = conclusion
	}
	status["gate"] = map[string]any{"job_id": gateJob, "job_generation": gateJobGen, "roll_generation": gateRollGen, "rejected_ranges": gateRejected, "rejected_lots": gateRejections}
	return status
}
