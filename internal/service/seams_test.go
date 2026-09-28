package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-038/internal/model"
	"github.com/wyw14/solo-038/internal/splice"
)

func newStartedRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(Config{DataDir: t.TempDir(), FestoonCapacity: 120, RegisterGap: 500 * time.Millisecond, RegisterTolerance: 0.25})
	if err != nil {
		t.Fatalf("runtime construction failed: %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("runtime start failed: %v", err)
	}
	return runtime
}

func mustOperate(t *testing.T, runtime *Runtime, kind string) {
	t.Helper()
	if _, err := runtime.CreateOperation(context.Background(), kind); err != nil {
		t.Fatalf("operation %s failed: %v", kind, err)
	}
}

func TestSeamInspectionLifecycle(t *testing.T) {
	runtime := newStartedRuntime(t)
	if _, ok := runtime.CurrentJob(); !ok {
		t.Fatal("expected an active job")
	}

	// First inspection: overlap short and pull weak — the seam is condemned.
	mustOperate(t, runtime, "inspect_splice")
	report := runtime.SeamReport()
	if len(report.Certificates) != 1 {
		t.Fatalf("expected one certificate, got %d", len(report.Certificates))
	}
	first := report.Certificates[0]
	if first.Verdict != model.SeamRejected || first.Revision != 1 {
		t.Fatalf("first inspection must be rejected revision 1, got %+v", first)
	}
	wantReject := model.MeterRange{From: demoSeamMeter - splice.RejectMarginMeters, To: demoSeamMeter + splice.RejectMarginMeters}
	if len(report.RejectedRanges) != 1 || report.RejectedRanges[0] != wantReject {
		t.Fatalf("rejected ranges = %v, want [%v]", report.RejectedRanges, wantReject)
	}
	// The conclusion must have travelled press → dryer → folder.
	if len(report.Routes) != 1 || !report.Routes[0].Delivered {
		t.Fatalf("conclusion must be delivered along the web path, routes=%+v", report.Routes)
	}
	if len(report.Delivered) != 1 {
		t.Fatalf("folder must hold the delivered conclusion, got %d", len(report.Delivered))
	}

	// The lot window overlaps the condemned segment — finishing is blocked.
	if _, err := runtime.CreateOperation(context.Background(), "finish_output"); err == nil || !strings.Contains(err.Error(), "condemned") {
		t.Fatalf("finish must be blocked by condemned splice paper, got %v", err)
	}

	// Reinspection passes every criterion — the old reject must still stand.
	mustOperate(t, runtime, "reinspect_splice")
	report = runtime.SeamReport()
	if len(report.Certificates) != 1 || report.Certificates[0].Revision != 2 || report.Certificates[0].Verdict != model.SeamAccepted {
		t.Fatalf("current conclusion must be the accepted reinspection, got %+v", report.Certificates)
	}
	if len(report.RejectedRanges) != 1 || report.RejectedRanges[0] != wantReject {
		t.Fatalf("reinspection must not overwrite condemned segments: %v", report.RejectedRanges)
	}
	if len(report.Delivered) != 2 {
		t.Fatalf("both conclusions must reach the folder, got %d", len(report.Delivered))
	}
	if _, err := runtime.CreateOperation(context.Background(), "finish_output"); err == nil {
		t.Fatal("finish must stay blocked while condemned paper is on the web")
	}

	// Physical cut-out releases the lot; history stays condemned.
	mustOperate(t, runtime, "discard_seam_segments")
	mustOperate(t, runtime, "finish_output")
	report = runtime.SeamReport()
	if len(report.RejectedRanges) != 1 || report.RejectedRanges[0] != wantReject {
		t.Fatalf("cut-out must not rewrite splice history: %v", report.RejectedRanges)
	}
	if len(report.Discarded) != 1 || report.Discarded[0] != wantReject {
		t.Fatalf("discarded ranges = %v, want [%v]", report.Discarded, wantReject)
	}

	// Recovery replays the journal: revisions, deliveries and the condemned
	// segment are all rebuilt — nothing is overwritten.
	mustOperate(t, runtime, "recover")
	report = runtime.SeamReport()
	if len(report.Certificates) != 1 || report.Certificates[0].Revision != 2 {
		t.Fatalf("recovered certificates = %+v", report.Certificates)
	}
	if len(report.RejectedRanges) != 1 || report.RejectedRanges[0] != wantReject {
		t.Fatalf("recovery lost the condemned segment: %v", report.RejectedRanges)
	}
	if len(report.Discarded) != 1 {
		t.Fatalf("recovery lost the cut-out record: %v", report.Discarded)
	}
	if len(report.Delivered) != 2 {
		t.Fatalf("recovery lost delivered conclusions: %d", len(report.Delivered))
	}
	if len(report.Routes) != 2 {
		t.Fatalf("recovery must rebuild both web-path traces, got %d", len(report.Routes))
	}
}

func TestSeamReportWithoutJob(t *testing.T) {
	runtime, err := NewRuntime(Config{DataDir: t.TempDir(), FestoonCapacity: 120, RegisterGap: 500 * time.Millisecond, RegisterTolerance: 0.25})
	if err != nil {
		t.Fatalf("runtime construction failed: %v", err)
	}
	report := runtime.SeamReport()
	if len(report.Certificates) != 0 || len(report.RejectedRanges) != 0 {
		t.Fatalf("report without a job must be empty, got %+v", report)
	}
}
