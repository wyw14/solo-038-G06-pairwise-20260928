package splice

import (
	"sort"
	"sync"
	"time"

	"github.com/wyw14/solo-038/internal/journal"
	"github.com/wyw14/solo-038/internal/model"
)

type rollKey struct {
	job            model.JobID
	jobGeneration  uint64
	rollGeneration uint64
}

type rollLedger struct {
	history  []Inspection
	revision int
}

// Registry 是接缝检验台账。
//
// 复检语义：同一代际再次检验时，新检验证以更高 revision 追加进 history，
// 旧结论一律保留；必须剔除的纸段取历轮并集，单调累积，任何复检都不能缩小或清除它。
type Registry struct {
	mu      sync.RWMutex
	ledgers map[rollKey]*rollLedger
	journal *journal.Store
}

func NewRegistry(store *journal.Store) *Registry {
	return &Registry{ledgers: map[rollKey]*rollLedger{}, journal: store}
}

// Record 登记一轮检验（首检或复检）并持久化。返回带最终 revision 的检验证。
func (registry *Registry) Record(job model.Job, operation model.OperationID, input InspectionInput, now time.Time) (Inspection, error) {
	if err := job.Validate(); err != nil {
		return Inspection{}, err
	}
	key := rollKey{job: job.ID, jobGeneration: job.Generation, rollGeneration: job.RollGeneration}

	registry.mu.Lock()
	ledger := registry.ledgers[key]
	if ledger == nil {
		ledger = &rollLedger{}
		registry.ledgers[key] = ledger
	}
	revision := ledger.revision + 1
	registry.mu.Unlock()

	inspection, err := BuildInspection(job, operation, revision, input, now)
	if err != nil {
		return Inspection{}, err
	}

	if registry.journal != nil {
		if _, err := journal.PersistSeamInspection(registry.journal, job, toRecord(inspection), now); err != nil {
			return Inspection{}, err
		}
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	// 持久化可能乱序完成，落账时按 revision 归位，保证历史顺序与检验轮次一致。
	ledger.history = append(ledger.history, inspection)
	sort.Slice(ledger.history, func(left, right int) bool {
		return ledger.history[left].Revision < ledger.history[right].Revision
	})
	if revision > ledger.revision {
		ledger.revision = revision
	}
	return inspection, nil
}

// History 返回某代际下全部检验轮次（含首检与历次复检），按 revision 升序，旧结论原样保留。
func (registry *Registry) History(job model.Job) []Inspection {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	ledger := registry.ledgers[rollKey{job: job.ID, jobGeneration: job.Generation, rollGeneration: job.RollGeneration}]
	if ledger == nil {
		return nil
	}
	return append([]Inspection(nil), ledger.history...)
}

// Conclusion 汇总某代际沿纸路传给成品段的结论。
// RejectedRanges 为历轮剔除段并集；复检即使改判合格，也不会移除先前已剔除的段。
func (registry *Registry) Conclusion(job model.Job) (Conclusion, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	ledger := registry.ledgers[rollKey{job: job.ID, jobGeneration: job.Generation, rollGeneration: job.RollGeneration}]
	if ledger == nil || len(ledger.history) == 0 {
		return Conclusion{}, false
	}
	return ledger.conclusion(rollKey{job: job.ID, jobGeneration: job.Generation, rollGeneration: job.RollGeneration}), true
}

// AllConclusions 按代际排序返回全部结论，供快照/诊断使用。
func (registry *Registry) AllConclusions() []Conclusion {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	keys := make([]rollKey, 0, len(registry.ledgers))
	for key, ledger := range registry.ledgers {
		if len(ledger.history) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].job != keys[right].job {
			return keys[left].job < keys[right].job
		}
		if keys[left].jobGeneration != keys[right].jobGeneration {
			return keys[left].jobGeneration < keys[right].jobGeneration
		}
		return keys[left].rollGeneration < keys[right].rollGeneration
	})
	conclusions := make([]Conclusion, 0, len(keys))
	for _, key := range keys {
		conclusions = append(conclusions, registry.ledgers[key].conclusion(key))
	}
	return conclusions
}

func (ledger *rollLedger) conclusion(key rollKey) Conclusion {
	ranges := make([]model.MeterRange, 0)
	for _, inspection := range ledger.history {
		ranges = append(ranges, inspection.RejectedRanges...)
	}
	latest := ledger.history[len(ledger.history)-1]
	return Conclusion{
		JobID:          key.job,
		JobGeneration:  key.jobGeneration,
		RollGeneration: key.rollGeneration,
		RejectedRanges: model.MergeMeterRanges(ranges),
		LatestVerdict:  latest.Verdict,
		Revision:       latest.Revision,
	}
}

// Restore 用事件流回放结果重建台账。回放记录只重建内存态，不再重复写日志。
// 同一代际已有的在卷历史予以保留（旧结论不覆盖），回放记录按 revision 归并去重。
func (registry *Registry) Restore(recovered []journal.SeamInspectionRecord) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, record := range recovered {
		key := rollKey{job: record.JobID, jobGeneration: record.JobGeneration, rollGeneration: record.RollGeneration}
		ledger := registry.ledgers[key]
		if ledger == nil {
			ledger = &rollLedger{}
			registry.ledgers[key] = ledger
		}
		inspection := fromRecord(record)
		duplicate := false
		for _, existing := range ledger.history {
			if existing.ID == inspection.ID || existing.Revision == inspection.Revision {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		ledger.history = append(ledger.history, inspection)
		sort.Slice(ledger.history, func(left, right int) bool {
			return ledger.history[left].Revision < ledger.history[right].Revision
		})
		if inspection.Revision > ledger.revision {
			ledger.revision = inspection.Revision
		}
	}
}

func toRecord(inspection Inspection) journal.SeamInspectionRecord {
	return journal.SeamInspectionRecord{
		InspectionID:   inspection.ID,
		JobID:          inspection.JobID,
		JobGeneration:  inspection.JobGeneration,
		RollGeneration: inspection.RollGeneration,
		Operation:      inspection.Operation,
		Revision:       inspection.Revision,
		SeamMeter:      inspection.SeamMeter,
		OverlapMM:      inspection.OverlapMM,
		PullN:          inspection.PullN,
		Limits: journal.SeamLimitsRecord{
			MinOverlapMM: inspection.Limits.MinOverlapMM,
			MinPullN:     inspection.Limits.MinPullN,
			RejectMargin: inspection.Limits.RejectMargin,
		},
		Verdict:        inspection.Verdict,
		AcceptedRange:  inspection.AcceptedRange,
		RejectedRanges: append([]model.MeterRange(nil), inspection.RejectedRanges...),
		InspectedAt:    inspection.InspectedAt,
	}
}

func fromRecord(record journal.SeamInspectionRecord) Inspection {
	return Inspection{
		ID:             record.InspectionID,
		JobID:          record.JobID,
		JobGeneration:  record.JobGeneration,
		RollGeneration: record.RollGeneration,
		Operation:      record.Operation,
		Revision:       record.Revision,
		SeamMeter:      record.SeamMeter,
		OverlapMM:      record.OverlapMM,
		PullN:          record.PullN,
		Limits: Limits{
			MinOverlapMM: record.Limits.MinOverlapMM,
			MinPullN:     record.Limits.MinPullN,
			RejectMargin: record.Limits.RejectMargin,
		},
		Verdict:        record.Verdict,
		AcceptedRange:  record.AcceptedRange,
		RejectedRanges: append([]model.MeterRange(nil), record.RejectedRanges...),
		InspectedAt:    record.InspectedAt,
	}
}
