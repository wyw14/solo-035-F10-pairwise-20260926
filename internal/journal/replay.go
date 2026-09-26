package journal

import (
	"fmt"

	"github.com/wyw14/solo-035/internal/model"
)

type ReplayResult struct {
	EventID          model.EventID    `json:"event_id"`
	Revision         model.RevisionID `json:"revision"`
	Fence            uint64           `json:"fence"`
	SourceGeneration uint64           `json:"source_generation"`
	Kinds            []string         `json:"kinds"`
}

func Replay(records []Record) ([]ReplayResult, error) {
	byEvent := make(map[model.EventID]*ReplayResult)
	order := make([]model.EventID, 0)
	for _, record := range records {
		if !record.EventID.Valid() {
			return nil, fmt.Errorf("journal record has invalid event")
		}
		result := byEvent[record.EventID]
		if result == nil {
			result = &ReplayResult{EventID: record.EventID}
			byEvent[record.EventID] = result
			order = append(order, record.EventID)
		}
		if record.Revision != "" {
			result.Revision = record.Revision
		}
		if record.Fence > result.Fence {
			result.Fence = record.Fence
		}
		if record.Kind == "lip-sync-proof" {
			var payload struct {
				SourceGeneration uint64 `json:"source_generation"`
			}
			if err := record.Decode(&payload); err == nil && payload.SourceGeneration > 0 {
				result.SourceGeneration = payload.SourceGeneration
			}
		}
		result.Kinds = append(result.Kinds, record.Kind)
	}
	items := make([]ReplayResult, 0, len(order))
	for _, eventID := range order {
		items = append(items, *byEvent[eventID])
	}
	return items, nil
}
