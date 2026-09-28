package folder

import (
	"testing"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

func printingJob(now time.Time) model.Job {
	job := model.NewJob(now)
	job.State = model.JobPrinting
	job.RollGeneration = 1
	return job
}

func TestGateBlocksRejectedLotAndPassesCleanLot(t *testing.T) {
	now := time.Now()
	job := printingJob(now)
	gate := NewGate()

	if err := gate.Receive(job.ID, job.Generation, job.RollGeneration, []model.MeterRange{{Start: 198, End: 202}}, 1); err != nil {
		t.Fatal(err)
	}
	if err := gate.Admit(job, model.MeterRange{Start: 0, End: 100}, now); err != nil {
		t.Fatalf("clean lot should be admitted: %v", err)
	}
	if err := gate.Admit(job, model.MeterRange{Start: 190, End: 210}, now); err == nil {
		t.Fatal("lot overlapping the rejected splice segment must be blocked")
	}
	_, _, _, rejected, rejections := gate.Snapshot()
	if len(rejections) != 1 {
		t.Fatalf("expected one rejected lot record, got %d", len(rejections))
	}
	if len(rejected) != 1 {
		t.Fatalf("expected one rejected range, got %v", rejected)
	}
}

func TestGateReinspectionCannotClearRejectedSegment(t *testing.T) {
	now := time.Now()
	job := printingJob(now)
	gate := NewGate()

	if err := gate.Receive(job.ID, job.Generation, job.RollGeneration, []model.MeterRange{{Start: 198, End: 202}}, 1); err != nil {
		t.Fatal(err)
	}
	// 复检改判合格，沿纸路不再携带剔除段；闸门必须保留已剔除的 198-202m。
	if err := gate.Receive(job.ID, job.Generation, job.RollGeneration, nil, 2); err != nil {
		t.Fatal(err)
	}
	if err := gate.Admit(job, model.MeterRange{Start: 200, End: 201}, now); err == nil {
		t.Fatal("a passing reinspection must not clear the already rejected segment")
	}

	// 又一条接缝不合格：并集累积。
	if err := gate.Receive(job.ID, job.Generation, job.RollGeneration, []model.MeterRange{{Start: 298, End: 302}}, 3); err != nil {
		t.Fatal(err)
	}
	if err := gate.Admit(job, model.MeterRange{Start: 299, End: 300}, now); err == nil {
		t.Fatal("newly rejected segment must also block the finished lot")
	}
}

func TestGateResetsForNewRollGeneration(t *testing.T) {
	now := time.Now()
	job := printingJob(now)
	gate := NewGate()
	if err := gate.Receive(job.ID, job.Generation, job.RollGeneration, []model.MeterRange{{Start: 198, End: 202}}, 1); err != nil {
		t.Fatal(err)
	}
	nextRoll := job.ChangeRoll(now)
	if err := gate.Receive(nextRoll.ID, nextRoll.Generation, nextRoll.RollGeneration, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := gate.Admit(nextRoll, model.MeterRange{Start: 200, End: 201}, now); err != nil {
		t.Fatalf("new roll generation must not be bound by the old roll's rejected segments: %v", err)
	}
}
