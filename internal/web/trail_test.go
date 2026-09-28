package web

import (
	"testing"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

func TestPaperTrailPublishesAndKeepsHistory(t *testing.T) {
	jobID := model.NewJobID()
	trail := NewPaperTrail()
	now := time.Now()

	notice := TrailNotice{JobID: jobID, JobGeneration: 1, RollGeneration: 1, RejectedRanges: []model.MeterRange{{Start: 198, End: 202}}, LatestVerdict: "rejected", Revision: 1, ForwardedAt: now}
	if err := trail.Publish(notice); err != nil {
		t.Fatal(err)
	}
	// 复检公告：剔除段并集后下发，旧公告仍保留。
	recheck := TrailNotice{JobID: jobID, JobGeneration: 1, RollGeneration: 1, RejectedRanges: []model.MeterRange{{Start: 298, End: 302}, {Start: 200, End: 201}}, LatestVerdict: "accepted", Revision: 2, ForwardedAt: now.Add(time.Minute)}
	if err := trail.Publish(recheck); err != nil {
		t.Fatal(err)
	}

	current, ok := trail.Current(jobID, 1, 1)
	if !ok {
		t.Fatal("expected current notice")
	}
	if current.Revision != 2 || current.LatestVerdict != "accepted" {
		t.Fatalf("expected latest reinspection notice, got rev %d verdict %q", current.Revision, current.LatestVerdict)
	}
	if len(current.RejectedRanges) != 2 {
		t.Fatalf("expected rejected union carried downstream, got %v", current.RejectedRanges)
	}
	if len(trail.Notices()) != 2 {
		t.Fatal("paper trail must retain the old notice instead of overwriting it")
	}
	if _, ok := trail.Current(jobID, 1, 2); ok {
		t.Fatal("new roll generation must not see the previous roll's notice")
	}
}

func TestPaperTrailRejectsInvalidNotice(t *testing.T) {
	trail := NewPaperTrail()
	if err := trail.Publish(TrailNotice{JobGeneration: 1, RollGeneration: 1, LatestVerdict: "rejected", Revision: 1}); err == nil {
		t.Fatal("expected error for missing job id")
	}
	notice := TrailNotice{JobID: model.NewJobID(), JobGeneration: 1, RollGeneration: 1, LatestVerdict: "rejected", Revision: 1, RejectedRanges: []model.MeterRange{{Start: 10, End: 5}}}
	if err := trail.Publish(notice); err == nil {
		t.Fatal("expected error for invalid rejected range")
	}
}
