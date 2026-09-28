package splice

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wyw14/solo-038/internal/journal"
	"github.com/wyw14/solo-038/internal/model"
)

func testJob(now time.Time) model.Job {
	job := model.NewJob(now)
	job.State = model.JobPrinting
	job.RollGeneration = 1
	return job
}

func testLimits() Limits {
	return Limits{MinOverlapMM: 40, MinPullN: 60, RejectMargin: 2}
}

func TestBuildInspectionAcceptsAndRejects(t *testing.T) {
	now := time.Now()
	job := testJob(now)

	good, err := BuildInspection(job, model.NewOperationID(), 1, InspectionInput{SeamMeter: 200, OverlapMM: 45, PullN: 80, Limits: testLimits()}, now)
	if err != nil {
		t.Fatal(err)
	}
	if good.Verdict != VerdictAccepted || !good.Passed() {
		t.Fatalf("expected accepted verdict, got %q", good.Verdict)
	}
	if good.AcceptedRange != (model.MeterRange{Start: 198, End: 202}) {
		t.Fatalf("unexpected accepted range %+v", good.AcceptedRange)
	}
	if len(good.RejectedRanges) != 0 {
		t.Fatalf("accepted seam must carry no rejected ranges, got %v", good.RejectedRanges)
	}

	bad, err := BuildInspection(job, model.NewOperationID(), 1, InspectionInput{SeamMeter: 200, OverlapMM: 30, PullN: 80, Limits: testLimits()}, now)
	if err != nil {
		t.Fatal(err)
	}
	if bad.Verdict != VerdictRejected || bad.Passed() {
		t.Fatalf("expected rejected verdict for narrow overlap, got %q", bad.Verdict)
	}
	if len(bad.RejectedRanges) != 1 || bad.RejectedRanges[0] != (model.MeterRange{Start: 198, End: 202}) {
		t.Fatalf("unexpected rejected ranges %v", bad.RejectedRanges)
	}

	weak, err := BuildInspection(job, model.NewOperationID(), 1, InspectionInput{SeamMeter: 1, OverlapMM: 50, PullN: 40, Limits: testLimits()}, now)
	if err != nil {
		t.Fatal(err)
	}
	if weak.Verdict != VerdictRejected {
		t.Fatalf("expected rejected verdict for weak pull, got %q", weak.Verdict)
	}
	if weak.RejectedRanges[0].Start != 0 || weak.RejectedRanges[0].End != 3 {
		t.Fatalf("expected start clamped to zero, got %+v", weak.RejectedRanges[0])
	}
}

func TestBuildInspectionRejectsBadInput(t *testing.T) {
	now := time.Now()
	job := testJob(now)
	if _, err := BuildInspection(job, model.NewOperationID(), 1, InspectionInput{SeamMeter: 10, OverlapMM: 0, PullN: 80, Limits: testLimits()}, now); err == nil {
		t.Fatal("expected error for missing overlap")
	}
	if _, err := BuildInspection(job, model.NewOperationID(), 1, InspectionInput{SeamMeter: 10, OverlapMM: 50, PullN: 80, Limits: Limits{}}, now); err == nil {
		t.Fatal("expected error for incomplete limits")
	}
}

func TestRegistryReinspectionKeepsOldRejectedSegments(t *testing.T) {
	now := time.Now()
	job := testJob(now)
	registry := NewRegistry(nil)

	first, err := registry.Record(job, model.NewOperationID(), InspectionInput{SeamMeter: 200, OverlapMM: 30, PullN: 80, Limits: testLimits()}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 {
		t.Fatalf("expected first revision, got %d", first.Revision)
	}

	// 复检改判合格：新检验证合格，但 198-202m 已剔除段必须保留。
	second, err := registry.Record(job, model.NewOperationID(), InspectionInput{SeamMeter: 200, OverlapMM: 50, PullN: 90, Limits: testLimits()}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || !second.Passed() {
		t.Fatalf("expected a passing revision 2, got revision %d verdict %q", second.Revision, second.Verdict)
	}

	history := registry.History(job)
	if len(history) != 2 {
		t.Fatalf("expected both inspection rounds kept in history, got %d", len(history))
	}
	if history[0].ID == history[1].ID {
		t.Fatal("reinspection must create a new evidence record, not overwrite the old one")
	}
	if history[0].Verdict != VerdictRejected {
		t.Fatal("old rejected verdict must be preserved")
	}

	conclusion, ok := registry.Conclusion(job)
	if !ok {
		t.Fatal("expected a conclusion")
	}
	if conclusion.LatestVerdict != VerdictAccepted || conclusion.Revision != 2 {
		t.Fatalf("expected latest verdict accepted at revision 2, got %q rev %d", conclusion.LatestVerdict, conclusion.Revision)
	}
	if len(conclusion.RejectedRanges) != 1 || conclusion.RejectedRanges[0] != (model.MeterRange{Start: 198, End: 202}) {
		t.Fatalf("previously rejected segment must survive a passing reinspection, got %v", conclusion.RejectedRanges)
	}
	if !conclusion.RejectedAt(200) || conclusion.RejectedAt(203) {
		t.Fatal("RejectedAt did not reflect the preserved rejected segment")
	}

	// 复检在另一接缝又发现不合格：剔除段单调并集累积。
	third, err := registry.Record(job, model.NewOperationID(), InspectionInput{SeamMeter: 300, OverlapMM: 20, PullN: 90, Limits: testLimits()}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if third.Passed() {
		t.Fatal("expected third round to reject the new seam")
	}
	conclusion, _ = registry.Conclusion(job)
	if len(conclusion.RejectedRanges) != 2 {
		t.Fatalf("expected two cumulative rejected segments, got %v", conclusion.RejectedRanges)
	}
}

func TestRegistryIsolatesRollGenerations(t *testing.T) {
	now := time.Now()
	job := testJob(now)
	registry := NewRegistry(nil)
	if _, err := registry.Record(job, model.NewOperationID(), InspectionInput{SeamMeter: 200, OverlapMM: 30, PullN: 80, Limits: testLimits()}, now); err != nil {
		t.Fatal(err)
	}
	nextRoll := job.ChangeRoll(now)
	if _, ok := registry.Conclusion(nextRoll); ok {
		t.Fatal("new roll generation must not inherit the previous roll's splice conclusion")
	}
}

func TestRegistryPersistsAndRestoresFromJournal(t *testing.T) {
	now := time.Now()
	job := testJob(now)
	store := journal.NewStore(filepath.Join(t.TempDir(), "events.jsonl"))
	registry := NewRegistry(store)

	if _, err := registry.Record(job, model.NewOperationID(), InspectionInput{SeamMeter: 200, OverlapMM: 30, PullN: 80, Limits: testLimits()}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Record(job, model.NewOperationID(), InspectionInput{SeamMeter: 200, OverlapMM: 55, PullN: 90, Limits: testLimits()}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	events, err := store.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != "splice_seam_inspected" {
		t.Fatalf("expected two splice_seam_inspected events, got %v", events)
	}
	recovered, err := journal.ReplaySeamInspections(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 2 {
		t.Fatalf("expected two replayed seam records, got %d", len(recovered))
	}

	rebuilt := NewRegistry(nil)
	rebuilt.Restore(recovered)
	history := rebuilt.History(job)
	if len(history) != 2 || history[0].Revision != 1 || history[1].Revision != 2 {
		t.Fatalf("restore must rebuild both rounds in revision order, got %v", history)
	}
	conclusion, ok := rebuilt.Conclusion(job)
	if !ok || len(conclusion.RejectedRanges) != 1 || conclusion.LatestVerdict != VerdictAccepted {
		t.Fatalf("restore must preserve the rejected union and latest verdict, got %+v", conclusion)
	}
}
