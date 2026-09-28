package folder

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-038/internal/model"
)

type Output struct {
	JobID          model.JobID        `json:"job_id"`
	JobGeneration  uint64             `json:"job_generation"`
	RollGeneration uint64             `json:"roll_generation"`
	Sheets         int                `json:"sheets"`
	InspectedSeams int                `json:"inspected_seams"`
	RejectedMeters []model.MeterRange `json:"rejected_meters"`
	CompletedAt    time.Time          `json:"completed_at"`
}

func BuildOutput(job model.Job, sheets int, rejectedMeters []model.MeterRange, inspectedSeams int, now time.Time) (Output, error) {
	if err := job.Validate(); err != nil {
		return Output{}, err
	}
	if sheets <= 0 {
		return Output{}, fmt.Errorf("folder output requires a positive sheet count")
	}
	return Output{JobID: job.ID, JobGeneration: job.Generation, RollGeneration: job.RollGeneration, Sheets: sheets, InspectedSeams: inspectedSeams, RejectedMeters: model.MergeMeterRanges(rejectedMeters), CompletedAt: now.UTC()}, nil
}

func (output Output) BelongsTo(job model.Job) bool {
	return output.JobID == job.ID && output.JobGeneration == job.Generation && output.RollGeneration == job.RollGeneration
}
