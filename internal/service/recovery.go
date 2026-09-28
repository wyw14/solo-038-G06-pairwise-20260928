package service

import (
	"context"
	"fmt"
	"time"

	"github.com/wyw14/solo-038/internal/journal"
	"github.com/wyw14/solo-038/internal/model"
	"github.com/wyw14/solo-038/internal/web"
)

type Cancellation struct {
	JobID       model.JobID `json:"job_id"`
	Generation  uint64      `json:"generation"`
	CancelledAt time.Time   `json:"cancelled_at"`
	Components  []string    `json:"components"`
}

func (runtime *Runtime) CancelCurrent(ctx context.Context) (Cancellation, error) {
	current, ok := runtime.jobs.Current()
	if !ok {
		return Cancellation{}, fmt.Errorf("there is no current job")
	}
	select {
	case <-ctx.Done():
		return Cancellation{}, ctx.Err()
	default:
	}
	if err := runtime.jobControl.Cancel(current, time.Now()); err != nil {
		return Cancellation{}, err
	}
	return Cancellation{JobID: current.ID, Generation: current.Generation, CancelledAt: time.Now().UTC(), Components: []string{"job", "dryer", "web"}}, nil
}

func (runtime *Runtime) SaveSnapshot() error {
	current, ok := runtime.jobs.Current()
	if !ok {
		return fmt.Errorf("there is no current job")
	}
	owner, jobGeneration, rollGeneration, threaded, meter := runtime.web.Snapshot()
	values := map[string]any{"job": current, "web_owner": owner, "web_job_generation": jobGeneration, "web_roll_generation": rollGeneration, "web_threaded": threaded, "web_meter": meter}
	return runtimeSaveSnapshot(runtime.config.DataDir, values)
}

// RecoverSpliceInspections 从事件流重建接缝检验台账，并把各代际最新结论
// 重新沿纸路送达成品段闸门。复检历史与已剔除纸段随事件原样恢复，不被覆盖。
func (runtime *Runtime) RecoverSpliceInspections() (int, error) {
	events, err := runtime.journal.Events()
	if err != nil {
		return 0, err
	}
	records, err := journal.ReplaySeamInspections(events)
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	runtime.spliceInspections.Restore(records)
	now := time.Now().UTC()
	for _, conclusion := range runtime.spliceInspections.AllConclusions() {
		notice := web.TrailNotice{JobID: conclusion.JobID, JobGeneration: conclusion.JobGeneration, RollGeneration: conclusion.RollGeneration, RejectedRanges: conclusion.RejectedRanges, LatestVerdict: conclusion.LatestVerdict, Revision: conclusion.Revision, ForwardedAt: now}
		if err := runtime.paperTrail.Publish(notice); err != nil {
			return 0, err
		}
		if err := runtime.spliceGate.Receive(conclusion.JobID, conclusion.JobGeneration, conclusion.RollGeneration, conclusion.RejectedRanges, conclusion.Revision); err != nil {
			return 0, err
		}
	}
	return len(records), nil
}
