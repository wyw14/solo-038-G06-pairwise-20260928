package journal

import (
	"fmt"
	"sort"

	"github.com/wyw14/solo-038/internal/model"
)

type RecoveredState struct {
	Job             model.Job
	Owner           string
	ReleasedRanges  []model.Interval
	SeamInspections []SeamInspectionRecord
	EventCount      int
}

func Replay(events []model.Event) (RecoveredState, error) {
	ordered := append([]model.Event(nil), events...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].At.Before(ordered[right].At)
	})
	state := RecoveredState{}
	for _, event := range ordered {
		if err := event.Validate(); err != nil {
			return RecoveredState{}, err
		}
		state.EventCount++
		switch event.Kind {
		case "job_created", "job_snapshot":
			job, err := model.DecodeEventPayload[model.Job](event)
			if err != nil {
				return RecoveredState{}, err
			}
			state.Job = job
		case "web_fence_restored":
			payload, err := model.DecodeEventPayload[map[string]any](event)
			if err != nil {
				return RecoveredState{}, err
			}
			state.Owner, _ = payload["owner"].(string)
		case "interval_committed":
			record, err := model.DecodeEventPayload[IntervalRecord](event)
			if err != nil {
				return RecoveredState{}, err
			}
			if record.Released {
				state.ReleasedRanges = append(state.ReleasedRanges, record.Interval)
			}
		case "splice_seam_inspected":
			record, err := model.DecodeEventPayload[SeamInspectionRecord](event)
			if err != nil {
				return RecoveredState{}, err
			}
			state.SeamInspections = append(state.SeamInspections, record)
		}
	}
	if state.EventCount > 0 && !state.Job.ID.Valid() {
		return RecoveredState{}, fmt.Errorf("journal has no recoverable job snapshot")
	}
	return state, nil
}
