package service

import (
	"context"
	"testing"
	"time"
)

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(Config{DataDir: t.TempDir(), FestoonCapacity: 120, RegisterGap: 500 * time.Millisecond, RegisterTolerance: 0.25})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return runtime
}

// feedUntil 直接在已穿好的纸路围栏上推进米位，模拟走纸到目标米位。
func feedUntil(t *testing.T, runtime *Runtime, owner string, targetMeter float64) {
	t.Helper()
	_, _, _, threaded, meter := runtime.web.Snapshot()
	if !threaded {
		t.Fatalf("web must be threaded before feeding (meter=%v)", meter)
	}
	if targetMeter > meter {
		if err := runtime.web.Advance(owner, targetMeter-meter); err != nil {
			t.Fatalf("web advance to %v failed: %v", targetMeter, err)
		}
	}
}

func TestRejectedSpliceSegmentBlocksFinishedOutput(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()
	if err := runtime.ExecuteOperation(ctx, "load_roll"); err != nil {
		t.Fatal(err)
	}
	job, _ := runtime.CurrentJob()

	// 首检：200m 处接缝搭接不足，判定 198-202m 必须剔除。
	rejected, err := runtime.InspectSplice(ctx, SeamInspectionRequest{SeamMeter: 200, OverlapMM: 30, PullN: 80})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Inspection.Passed() {
		t.Fatal("expected the seam to be rejected")
	}
	if _, ok := runtime.paperTrail.Current(rejected.Notice.JobID, rejected.Notice.JobGeneration, rejected.Notice.RollGeneration); !ok {
		t.Fatal("rejected conclusion must be forwarded along the paper path")
	}

	// 纸批 0-315m 覆盖剔除段，成品段必须拒绝放行。
	feedUntil(t, runtime, string(job.ID), 315)
	if err := runtime.ExecuteOperation(ctx, "finish_output"); err == nil {
		t.Fatal("finish_output must be blocked while the lot overlaps a rejected splice segment")
	}
}

func TestPassingReinspectionDoesNotClearRejectedSegment(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()
	if err := runtime.ExecuteOperation(ctx, "load_roll"); err != nil {
		t.Fatal(err)
	}
	job, _ := runtime.CurrentJob()

	if _, err := runtime.InspectSplice(ctx, SeamInspectionRequest{SeamMeter: 200, OverlapMM: 30, PullN: 80}); err != nil {
		t.Fatal(err)
	}
	// 复检合格：旧检验证保留，200m 处剔除段仍拦截成品批。
	recheck, err := runtime.InspectSplice(ctx, SeamInspectionRequest{SeamMeter: 200, OverlapMM: 50, PullN: 90})
	if err != nil {
		t.Fatal(err)
	}
	if !recheck.Inspection.Passed() || recheck.Inspection.Revision != 2 {
		t.Fatalf("expected a passing revision 2, got %+v", recheck.Inspection)
	}
	if len(recheck.History) != 2 || recheck.History[0].Verdict != "rejected" {
		t.Fatalf("old rejected evidence must be retained in history, got %v", recheck.History)
	}
	if len(recheck.Conclusion.RejectedRanges) != 1 {
		t.Fatalf("rejected segment must survive a passing reinspection, got %v", recheck.Conclusion.RejectedRanges)
	}

	feedUntil(t, runtime, string(job.ID), 315)
	if err := runtime.ExecuteOperation(ctx, "finish_output"); err == nil {
		t.Fatal("rejected segment remains binding even after a passing reinspection")
	}

	// 纸路走过剔除段后（纸批 293m 起），成品段恢复放行。
	feedUntil(t, runtime, string(job.ID), 608)
	if err := runtime.ExecuteOperation(ctx, "finish_output"); err != nil {
		t.Fatalf("finish_output should pass once the lot is clear of rejected segments: %v", err)
	}
}

func TestAcceptedSpliceDoesNotBlockOutput(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()
	if err := runtime.ExecuteOperation(ctx, "load_roll"); err != nil {
		t.Fatal(err)
	}
	job, _ := runtime.CurrentJob()
	accepted, err := runtime.InspectSplice(ctx, SeamInspectionRequest{SeamMeter: 200, OverlapMM: 48, PullN: 90})
	if err != nil {
		t.Fatal(err)
	}
	if !accepted.Inspection.Passed() || len(accepted.Conclusion.RejectedRanges) != 0 {
		t.Fatalf("accepted seam must not create rejected ranges, got %+v", accepted)
	}
	feedUntil(t, runtime, string(job.ID), 315)
	if err := runtime.ExecuteOperation(ctx, "finish_output"); err != nil {
		t.Fatalf("accepted seam must not block finished output: %v", err)
	}
}

func TestRecoverSpliceInspectionsRebuildsLedgerAndGate(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()
	if err := runtime.ExecuteOperation(ctx, "load_roll"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.InspectSplice(ctx, SeamInspectionRequest{SeamMeter: 200, OverlapMM: 30, PullN: 80}); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：用同一数据目录新建 runtime，从事件流重建台账并重新沿纸路送达闸门。
	restarted, err := NewRuntime(Config{DataDir: runtime.config.DataDir, FestoonCapacity: 120, RegisterGap: 500 * time.Millisecond, RegisterTolerance: 0.25})
	if err != nil {
		t.Fatal(err)
	}
	count, err := restarted.RecoverSpliceInspections()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one recovered inspection, got %d", count)
	}
	conclusions := restarted.spliceInspections.AllConclusions()
	if len(conclusions) != 1 || len(conclusions[0].RejectedRanges) != 1 {
		t.Fatalf("recovery must rebuild the rejected splice segment, got %v", conclusions)
	}
	if _, _, _, rejected, _ := restarted.spliceGate.Snapshot(); len(rejected) != 1 {
		t.Fatalf("recovery must re-forward the conclusion to the finished-section gate, got %v", rejected)
	}
	if len(restarted.paperTrail.Notices()) != 1 {
		t.Fatal("recovery must republish the conclusion along the paper path")
	}
}
