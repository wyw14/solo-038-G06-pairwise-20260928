package folder

import (
	"testing"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

func testJob() model.Job {
	job := model.NewJob(time.Now())
	job.RollGeneration = 1
	job.PlateGeneration = 1
	return job
}

func notice(job model.Job, certificateID model.EventID, verdict model.SeamVerdict, rejectRange model.MeterRange, union []model.MeterRange) SeamNotice {
	return SeamNotice{
		JobID:          job.ID,
		JobGeneration:  job.Generation,
		RollGeneration: job.RollGeneration,
		CertificateID:  certificateID,
		SeamID:         "seam-1",
		Revision:       1,
		Verdict:        verdict,
		RejectRange:    rejectRange,
		RejectedRanges: union,
	}
}

func TestQualityGateBlocksAndClearsLots(t *testing.T) {
	job := testJob()
	gate := NewQualityGate()
	rejected := model.MeterRange{From: 115, To: 125}
	if err := gate.Receive(notice(job, model.NewEventID(), model.SeamRejected, rejected, []model.MeterRange{rejected})); err != nil {
		t.Fatalf("receive failed: %v", err)
	}
	if cleared, blocked := gate.ClearLot(job, model.MeterRange{From: 100, To: 200}); cleared || len(blocked) != 1 || blocked[0] != rejected {
		t.Fatalf("lot overlapping condemned paper must be blocked: cleared=%v blocked=%v", cleared, blocked)
	}
	if cleared, _ := gate.ClearLot(job, model.MeterRange{From: 200, To: 300}); !cleared {
		t.Fatal("lot clear of condemned paper must pass")
	}
	// A passing reinspection arrives later — the condemned union must not shrink.
	if err := gate.Receive(notice(job, model.NewEventID(), model.SeamAccepted, model.MeterRange{}, []model.MeterRange{rejected})); err != nil {
		t.Fatalf("receive reinspection failed: %v", err)
	}
	if cleared, blocked := gate.ClearLot(job, model.MeterRange{From: 100, To: 200}); cleared || len(blocked) != 1 {
		t.Fatalf("passing reinspection must not unblock condemned paper: cleared=%v blocked=%v", cleared, blocked)
	}
	// Physical cut-out releases the gate without touching history.
	if _, err := gate.Discard(job, []model.MeterRange{rejected}); err != nil {
		t.Fatalf("discard failed: %v", err)
	}
	if cleared, _ := gate.ClearLot(job, model.MeterRange{From: 100, To: 200}); !cleared {
		t.Fatal("lot must clear once condemned paper is physically cut out")
	}
	if got := gate.RejectedRanges(job); len(got) != 1 || got[0] != rejected {
		t.Fatalf("condemned history must survive the cut-out: %v", got)
	}
}

func TestQualityGateRejectsLaundering(t *testing.T) {
	job := testJob()
	gate := NewQualityGate()
	rejected := model.MeterRange{From: 115, To: 125}
	if err := gate.Receive(notice(job, model.NewEventID(), model.SeamRejected, rejected, []model.MeterRange{rejected})); err != nil {
		t.Fatalf("receive failed: %v", err)
	}
	if _, err := gate.Discard(job, []model.MeterRange{{From: 200, To: 210}}); err == nil {
		t.Fatal("discarding paper that was never condemned must be rejected")
	}
	if _, err := gate.Discard(job, []model.MeterRange{{From: 110, To: 130}}); err == nil {
		t.Fatal("discarding beyond the condemned union must be rejected")
	}
}

func TestQualityGateIsFencedPerRoll(t *testing.T) {
	job := testJob()
	gate := NewQualityGate()
	rejected := model.MeterRange{From: 115, To: 125}
	if err := gate.Receive(notice(job, model.NewEventID(), model.SeamRejected, rejected, []model.MeterRange{rejected})); err != nil {
		t.Fatalf("receive failed: %v", err)
	}
	nextRoll := job.ChangeRoll(time.Now())
	if cleared, _ := gate.ClearLot(nextRoll, model.MeterRange{From: 100, To: 200}); !cleared {
		t.Fatal("condemned paper from a previous roll must not block the new roll")
	}
	// Duplicate delivery of the same certificate is a no-op.
	dup := notice(job, model.NewEventID(), model.SeamRejected, rejected, []model.MeterRange{rejected})
	if err := gate.Receive(dup); err != nil {
		t.Fatalf("receive failed: %v", err)
	}
	if err := gate.Receive(dup); err != nil {
		t.Fatalf("duplicate delivery must be idempotent: %v", err)
	}
	if got := gate.Notices(); len(got) != 2 {
		t.Fatalf("expected 2 notices, got %d", len(got))
	}
}
