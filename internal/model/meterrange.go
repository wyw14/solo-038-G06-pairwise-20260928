package model

import (
	"errors"
	"sort"
)

// MeterRange 表示纸路上一段连续的米位区间（闭开区间 [Start, End)）。
// 它同时用于接缝检验证里的合格区/剔除段，以及沿纸路传给成品段的剔除标记。
//
// 注意：这是独立于 Interval 的工作域类型——Interval 必须有打开/关闭时间且端值严格，
// 而米段允许零长度（恰好落在某一米位的接缝）并按代际隔离，因此不复用 Interval.Validate。
type MeterRange struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Valid 只约束端值次序，允许零长度段（Start == End），便于以点接缝为中心扩展剔除段。
func (meterRange MeterRange) Valid() bool {
	return meterRange.Start >= 0 && meterRange.End >= meterRange.Start
}

// MergeMeterRanges 把若干米段合并为互不相交、按起点排序的并集。
// 复检只追加结论、不覆盖旧结论，因此剔除段以并集方式单调累积。
func MergeMeterRanges(ranges []MeterRange) []MeterRange {
	valid := make([]MeterRange, 0, len(ranges))
	for _, meterRange := range ranges {
		// 零长度段不承载任何纸长，合并并集时直接丢弃。
		if meterRange.Valid() && meterRange.Start != meterRange.End {
			valid = append(valid, meterRange)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	sort.Slice(valid, func(left, right int) bool {
		if valid[left].Start != valid[right].Start {
			return valid[left].Start < valid[right].Start
		}
		return valid[left].End < valid[right].End
	})
	merged := make([]MeterRange, 0, len(valid))
	current := valid[0]
	for _, next := range valid[1:] {
		if next.Start <= current.End {
			if next.End > current.End {
				current.End = next.End
			}
			continue
		}
		merged = append(merged, current)
		current = next
	}
	merged = append(merged, current)
	return merged
}

// OverlapsMeterRange 判断点 meter 是否落在米段内（闭开区间）。
func (meterRange MeterRange) Contains(meter float64) bool {
	return meter >= meterRange.Start && meter < meterRange.End
}

// Overlaps 判断两个米段是否相交（零长度段不与任何段相交）。
func (meterRange MeterRange) Overlaps(other MeterRange) bool {
	if !meterRange.Valid() || !other.Valid() {
		return false
	}
	if meterRange.Start == meterRange.End || other.Start == other.End {
		return false
	}
	return meterRange.Start < other.End && other.Start < meterRange.End
}

// ErrMeterRangeRejected 由传播层在必须剔除的纸段试图放行成品时返回。
var ErrMeterRangeRejected = errors.New("meter range is marked for rejection")
