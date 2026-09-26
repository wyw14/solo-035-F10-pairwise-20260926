package playout

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/journal"
	"github.com/wyw14/solo-035/internal/model"
)

type Recovery struct {
	state *State
	clock func() time.Time
}

func NewRecovery(state *State, clock func() time.Time) *Recovery {
	return &Recovery{state: state, clock: clock}
}

func (recovery *Recovery) Restore(result journal.ReplayResult, source model.SourceID, segment string) error {
	if result.Revision == "" || result.Fence == 0 {
		return fmt.Errorf("playout recovery requires program revision and fence")
	}
	output := Output{
		EventID:          result.EventID,
		Revision:         result.Revision,
		Fence:            result.Fence,
		Source:           source,
		SourceGeneration: result.SourceGeneration,
		Segment:          segment,
		UpdatedAt:        recovery.clock().UTC(),
	}
	if err := recovery.state.Stage(output); err != nil {
		return err
	}
	return recovery.state.Publish(output.EventID, output.Revision, output.Fence, recovery.clock())
}

func (recovery *Recovery) RestoreAll(results []journal.ReplayResult, sourceFor func(model.EventID) model.SourceID) error {
	for _, result := range results {
		if err := recovery.Restore(result, sourceFor(result.EventID), "recovered-current-segment"); err != nil {
			return err
		}
	}
	return nil
}
