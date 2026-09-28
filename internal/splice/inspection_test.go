package splice

import (
	"testing"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

func measurement(overlapMM, pullForceN float64) SeamMeasurement {
	return SeamMeasurement{
		SeamID:         "seam-1",
		JobID:          model.NewJobID(),
		JobGeneration:  1,
		RollGeneration: 1,
		Meter:          120,
		OverlapMM:      overlapMM,
		PullForceN:     pullForceN,
	}
}

func TestInspectAcceptsWithinCriteria(t *testing.T) {
	certificate, err := Inspect(measurement(50, 200), time.Now())
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if certificate.Verdict != model.SeamAccepted {
		t.Fatalf("expected accepted verdict, got %s", certificate.Verdict)
	}
	if certificate.RejectRange.Valid() {
		t.Fatalf("accepted certificate must not carry a reject range: %v", certificate.RejectRange)
	}
}

func TestInspectRejectsOutOfCriteria(t *testing.T) {
	certificate, err := Inspect(measurement(35, 120), time.Now())
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if certificate.Verdict != model.SeamRejected {
		t.Fatalf("expected rejected verdict, got %s", certificate.Verdict)
	}
	if len(certificate.Reasons) != 2 {
		t.Fatalf("expected overlap and pull force reasons, got %v", certificate.Reasons)
	}
	want := model.MeterRange{From: 115, To: 125}
	if certificate.RejectRange != want {
		t.Fatalf("reject range = %v, want %v", certificate.RejectRange, want)
	}
}

func TestRejectSpanClampsAtZero(t *testing.T) {
	m := measurement(50, 200)
	m.Meter = 2
	if span := RejectSpan(m); span != (model.MeterRange{From: 0, To: 7}) {
		t.Fatalf("reject span = %v, want [0,7)", span)
	}
}

func TestLedgerReinspectionKeepsOldConclusions(t *testing.T) {
	ledger := NewInspectionLedger()
	m := measurement(35, 120)
	first, err := Inspect(m, time.Now())
	if err != nil {
		t.Fatalf("first inspect failed: %v", err)
	}
	first, err = ledger.Record(first)
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}
	if first.Revision != 1 || first.Supersedes != "" {
		t.Fatalf("first revision metadata wrong: %+v", first)
	}
	rejectedBefore := ledger.RejectedRanges(m.JobID, m.JobGeneration, m.RollGeneration)
	if len(rejectedBefore) != 1 || rejectedBefore[0] != (model.MeterRange{From: 115, To: 125}) {
		t.Fatalf("rejected ranges after first inspection = %v", rejectedBefore)
	}

	// Reinspection passes — the condemned segment must survive.
	second, err := Inspect(measurement(50, 200), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("reinspect failed: %v", err)
	}
	second, err = ledger.Record(second)
	if err != nil {
		t.Fatalf("record reinspection failed: %v", err)
	}
	if second.Revision != 2 || second.Supersedes != first.ID {
		t.Fatalf("reinspection must supersede the first revision: %+v", second)
	}
	if got := ledger.RejectedRanges(m.JobID, m.JobGeneration, m.RollGeneration); len(got) != 1 || got[0] != rejectedBefore[0] {
		t.Fatalf("rejected ranges were overwritten by reinspection: %v", got)
	}
	if !ledger.RejectedAt(m.JobID, m.JobGeneration, m.RollGeneration, 120) {
		t.Fatal("meter 120 must stay condemned after a passing reinspection")
	}
	if ledger.RejectedAt(m.JobID, m.JobGeneration, m.RollGeneration, 200) {
		t.Fatal("meter 200 was never condemned")
	}
	current, ok := ledger.Current("seam-1")
	if !ok || current.Verdict != model.SeamAccepted {
		t.Fatalf("current conclusion should be the accepted reinspection, got %+v", current)
	}
	if history := ledger.History("seam-1"); len(history) != 2 || history[0].Verdict != model.SeamRejected || history[1].Verdict != model.SeamAccepted {
		t.Fatalf("history must keep both conclusions in order, got %+v", history)
	}
}

func TestLedgerLotWindowSubtractsCondemned(t *testing.T) {
	ledger := NewInspectionLedger()
	m := measurement(35, 120)
	first, err := Inspect(m, time.Now())
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if _, err := ledger.Record(first); err != nil {
		t.Fatalf("record failed: %v", err)
	}
	accepted, rejected := ledger.LotWindow(m.JobID, m.JobGeneration, m.RollGeneration, model.MeterRange{From: 100, To: 200})
	if len(rejected) != 1 {
		t.Fatalf("expected one condemned range, got %v", rejected)
	}
	want := []model.MeterRange{{From: 100, To: 115}, {From: 125, To: 200}}
	if len(accepted) != 2 || accepted[0] != want[0] || accepted[1] != want[1] {
		t.Fatalf("accepted remainder = %v, want %v", accepted, want)
	}
}

func TestLedgerRestoreIsIdempotentAfterReset(t *testing.T) {
	ledger := NewInspectionLedger()
	m := measurement(35, 120)
	certificate, err := Inspect(m, time.Now())
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	certificate, err = ledger.Record(certificate)
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}
	ledger.Reset()
	if err := ledger.Restore([]model.SeamCertificate{certificate}); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got := ledger.RejectedRanges(m.JobID, m.JobGeneration, m.RollGeneration); len(got) != 1 {
		t.Fatalf("restored ledger lost the condemned segment: %v", got)
	}
}
