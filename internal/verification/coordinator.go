package verification

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type Coordinator struct {
	state *State
	clock func() time.Time
}

func NewCoordinator(state *State, clock func() time.Time) *Coordinator {
	return &Coordinator{state: state, clock: clock}
}

// PlanSource records the source identity planned during program preparation.
func (coordinator *Coordinator) PlanSource(eventID model.EventID, source model.SourceID, generation uint64, revision model.RevisionID, fence uint64) (Expected, error) {
	expected := Expected{
		EventID:    eventID,
		Source:     source,
		Generation: generation,
		Revision:   revision,
		Fence:      fence,
		PlannedAt:  coordinator.clock().UTC(),
	}
	if err := coordinator.state.Plan(expected); err != nil {
		return Expected{}, err
	}
	return expected, nil
}

func (coordinator *Coordinator) Invalidate(eventID model.EventID) {
	coordinator.state.Invalidate(eventID)
}

func (coordinator *Coordinator) InvalidateSource(sourceID model.SourceID) {
	coordinator.state.InvalidateSource(sourceID)
}

// Verify runs the comparison against the current plan and retains the result
// (pass or fail) as evidence.
func (coordinator *Coordinator) Verify(obs Observation) (Result, error) {
	expected, ok := coordinator.state.Expected(obs.EventID)
	if !ok {
		return Result{}, fmt.Errorf("event %s has no planned source identity", obs.EventID)
	}
	result := Evaluate(expected, obs, coordinator.clock())
	coordinator.state.Save(result)
	return result, nil
}

// CommitAssert is the on-air gate: a passing verification must exist, it must
// belong to the current plan, and the live observation must still match.
func (coordinator *Coordinator) CommitAssert(obs Observation) error {
	eventID := obs.EventID
	expected, ok := coordinator.state.Expected(eventID)
	if !ok {
		return fmt.Errorf("event %s has no planned source identity", eventID)
	}
	latest, ok := coordinator.state.Latest(eventID)
	if !ok {
		return fmt.Errorf("event %s source identity was never verified before going on air", eventID)
	}
	if !latest.Passed {
		return fmt.Errorf("on-air denied: %s", latest.Reason)
	}
	if latest.Fingerprint != expected.Fingerprint() {
		return fmt.Errorf("source plan changed after the last verification; re-verify before going on air")
	}
	if current := Evaluate(expected, obs, coordinator.clock()); !current.Passed {
		return fmt.Errorf("on-air denied: %s", current.Reason)
	}
	return nil
}

func (coordinator *Coordinator) Expected(eventID model.EventID) (Expected, bool) {
	return coordinator.state.Expected(eventID)
}

func (coordinator *Coordinator) Latest(eventID model.EventID) (Result, bool) {
	return coordinator.state.Latest(eventID)
}

func (coordinator *Coordinator) History(eventID model.EventID) []Result {
	return coordinator.state.History(eventID)
}
