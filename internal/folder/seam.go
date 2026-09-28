package folder

import (
	"fmt"
	"sort"
	"sync"

	"github.com/wyw14/solo-038/internal/model"
)

// SeamNotice is the seam conclusion handed from the web path to the folder.
// RejectedRanges carries the job-wide condemned union at hand-off time, so
// conclusions delivered earlier are never overwritten by later ones.
type SeamNotice struct {
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	CertificateID  model.EventID      `json:"certificate_id"`
	SeamID         string             `json:"seam_id"`
	Revision       int                `json:"revision"`
	Verdict        model.SeamVerdict  `json:"verdict"`
	RejectRange    model.MeterRange   `json:"reject_range"`
	RejectedRanges []model.MeterRange `json:"rejected_ranges"`
}

type fencedRanges struct {
	jobID          model.JobID
	jobGeneration  uint64
	rollGeneration uint64
	ranges         []model.MeterRange
}

func (fenced fencedRanges) matches(job model.Job) bool {
	return fenced.jobID == job.ID && fenced.jobGeneration == job.Generation && fenced.rollGeneration == job.RollGeneration
}

// QualityGate is the folder-side book of seam conclusions that arrived along
// the web path. Rejected segments accumulate as a union and are never removed:
// a lot whose meter window touches any condemned segment cannot be cleared.
// A condemned segment only leaves the blocking set once the operator records
// that it has been physically cut out — the splice ledger history is untouched.
type QualityGate struct {
	mu        sync.Mutex
	notices   []SeamNotice
	seen      map[model.EventID]bool
	rejected  []fencedRanges
	discarded []fencedRanges
}

func NewQualityGate() *QualityGate {
	return &QualityGate{seen: map[model.EventID]bool{}}
}

// Reset clears all in-memory conclusions before rebuilding from the journal.
func (gate *QualityGate) Reset() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.notices = nil
	gate.seen = map[model.EventID]bool{}
	gate.rejected = nil
	gate.discarded = nil
}

// Receive accepts a conclusion propagated down the web path. Delivery is
// idempotent per certificate; the condemned union only ever grows.
func (gate *QualityGate) Receive(notice SeamNotice) error {
	if !notice.JobID.Valid() || notice.JobGeneration == 0 || notice.RollGeneration == 0 {
		return fmt.Errorf("seam notice fence is incomplete")
	}
	if notice.CertificateID == "" || notice.SeamID == "" || notice.Revision < 1 {
		return fmt.Errorf("seam notice identity is incomplete")
	}
	if notice.Verdict != model.SeamAccepted && notice.Verdict != model.SeamRejected {
		return fmt.Errorf("seam notice verdict %q is invalid", notice.Verdict)
	}
	if notice.Verdict == model.SeamRejected && !notice.RejectRange.Valid() {
		return fmt.Errorf("rejected seam notice requires a reject range")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.seen[notice.CertificateID] {
		return nil
	}
	gate.seen[notice.CertificateID] = true
	gate.notices = append(gate.notices, notice)
	if len(notice.RejectedRanges) > 0 {
		gate.rejected = append(gate.rejected, fencedRanges{jobID: notice.JobID, jobGeneration: notice.JobGeneration, rollGeneration: notice.RollGeneration, ranges: model.MergeMeterRanges(notice.RejectedRanges)})
	}
	return nil
}

// Discard records that condemned segments have been physically cut out. Only
// segments inside the fence's condemned union may be discarded — an arbitrary
// range cannot be used to launder the gate.
func (gate *QualityGate) Discard(job model.Job, segments []model.MeterRange) ([]model.MeterRange, error) {
	if err := job.Validate(); err != nil {
		return nil, err
	}
	condemned := gate.rejectedFor(job)
	cut := []model.MeterRange{}
	for _, segment := range model.MergeMeterRanges(segments) {
		if !segment.Valid() {
			return nil, fmt.Errorf("discard segment is invalid")
		}
		covered := false
		for _, rejected := range condemned {
			if segment.From >= rejected.From && segment.To <= rejected.To {
				covered = true
				break
			}
		}
		if !covered {
			return nil, fmt.Errorf("segment %v was not condemned and cannot be discarded", segment)
		}
		cut = append(cut, segment)
	}
	if len(cut) == 0 {
		return nil, fmt.Errorf("no condemned segments were supplied")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.discarded = append(gate.discarded, fencedRanges{jobID: job.ID, jobGeneration: job.Generation, rollGeneration: job.RollGeneration, ranges: cut})
	return cut, nil
}

// RestoreDiscarded replays recorded physical removals during recovery.
func (gate *QualityGate) RestoreDiscarded(job model.Job, segments []model.MeterRange) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.discarded = append(gate.discarded, fencedRanges{jobID: job.ID, jobGeneration: job.Generation, rollGeneration: job.RollGeneration, ranges: model.MergeMeterRanges(segments)})
}

func (gate *QualityGate) rejectedFor(job model.Job) []model.MeterRange {
	union := []model.MeterRange{}
	for _, fenced := range gate.rejected {
		if fenced.matches(job) {
			union = append(union, fenced.ranges...)
		}
	}
	return model.MergeMeterRanges(union)
}

func (gate *QualityGate) discardedFor(job model.Job) []model.MeterRange {
	union := []model.MeterRange{}
	for _, fenced := range gate.discarded {
		if fenced.matches(job) {
			union = append(union, fenced.ranges...)
		}
	}
	return model.MergeMeterRanges(union)
}

// RejectedRanges returns the condemned union for one job fence.
func (gate *QualityGate) RejectedRanges(job model.Job) []model.MeterRange {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.rejectedFor(job)
}

// DiscardedRanges returns the physically removed union for one job fence.
func (gate *QualityGate) DiscardedRanges(job model.Job) []model.MeterRange {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.discardedFor(job)
}

// Notices returns every delivered conclusion in arrival order.
func (gate *QualityGate) Notices() []SeamNotice {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return append([]SeamNotice(nil), gate.notices...)
}

// InspectedSeams counts distinct delivered seams for one job fence.
func (gate *QualityGate) InspectedSeams(job model.Job) int {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	seen := map[string]bool{}
	for _, notice := range gate.notices {
		if notice.JobID == job.ID && notice.JobGeneration == job.Generation && notice.RollGeneration == job.RollGeneration {
			seen[notice.SeamID] = true
		}
	}
	return len(seen)
}

// ClearLot decides whether the finished lot's meter window is free of
// condemned paper that has not been physically removed. The blocking
// segments are returned for quarantine.
func (gate *QualityGate) ClearLot(job model.Job, window model.MeterRange) (bool, []model.MeterRange) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if !window.Valid() {
		return true, nil
	}
	outstanding := model.SubtractMeterRanges(gate.rejectedFor(job), gate.discardedFor(job))
	blocked := []model.MeterRange{}
	for _, meterRange := range outstanding {
		if window.Overlaps(meterRange) {
			blocked = append(blocked, meterRange)
		}
	}
	sort.Slice(blocked, func(left, right int) bool { return blocked[left].From < blocked[right].From })
	return len(blocked) == 0, blocked
}
