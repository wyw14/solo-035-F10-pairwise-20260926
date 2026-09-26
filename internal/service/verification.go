package service

import (
	"fmt"

	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/verification"
)

// observeSource collects the current receiver metadata, lock state and
// prepared output that are compared against the planned source identity.
func (coordinator *Coordinator) observeSource(event model.LiveEvent, expected verification.Expected) verification.Observation {
	runtime := coordinator.runtime
	obs := verification.Observation{
		EventID:               event.ID,
		EventRevision:         event.Revision,
		EventSourceGeneration: event.SourceGeneration,
	}
	// The per-event sync status records the source the event actually received
	// most recently, even after the receiver switches cameras.
	if status, ok := runtime.SyncState.Get(event.ID); ok {
		obs.Frame = verification.FrameEvidence{
			Present:    true,
			Source:     status.Source,
			Generation: status.SourceGeneration,
			Sequence:   status.LastSequence,
			Timecode:   status.LastTimecode,
			CapturedAt: status.UpdatedAt,
		}
	}
	if status, ok := runtime.Sources.Status(expected.Source); ok {
		obs.Lock = verification.LockEvidence{
			Present:        true,
			Source:         status.Source,
			Generation:     status.Generation,
			CurrentCommand: status.CurrentCommand,
			VideoLocked:    status.VideoLocked,
			AudioLocked:    status.AudioLocked,
		}
	}
	if frame, ok := runtime.Sources.LastFrame(expected.Source); ok {
		evidence := verification.FrameEvidence{
			Present:    true,
			Source:     frame.Source,
			Generation: frame.SourceGeneration,
			Sequence:   frame.Sequence,
			Timecode:   frame.Timecode,
			CapturedAt: frame.CapturedAt,
		}
		obs.ReceiverLastFrame = &evidence
	}
	if output, ok := runtime.Playout.Output(event.ID); ok {
		obs.Output = verification.OutputEvidence{
			Present:  true,
			EventID:  output.EventID,
			Source:   output.Source,
			Revision: output.Revision,
			Fence:    output.Fence,
			OnAir:    output.OnAir,
		}
	}
	return obs
}

// VerifySource compares the planned camera against the receiver's most recent
// frame metadata, current lock state and prepared output. Every attempt,
// including failures, is persisted as evidence.
func (coordinator *Coordinator) VerifySource(eventID model.EventID) (verification.Result, error) {
	runtime := coordinator.runtime
	event, ok := runtime.Events.Get(eventID)
	if !ok {
		return verification.Result{}, fmt.Errorf("event %s not found", eventID)
	}
	expected, hasPlan := runtime.Verification.Expected(eventID)
	var result verification.Result
	if !hasPlan {
		// A failed attempt still needs a complete audit trail; evaluate against
		// an empty plan so every observation is recorded.
		result = verification.Evaluate(verification.Expected{EventID: eventID}, coordinator.observeSource(event, verification.Expected{}), runtime.Clock())
		result.Expected.EventID = eventID
		result.Reason = "no expected source plan is recorded for this event"
	} else {
		obs := coordinator.observeSource(event, expected)
		var err error
		result, err = runtime.Verification.Verify(obs)
		if err != nil {
			return verification.Result{}, err
		}
	}
	// Record revision/fence against the CURRENT event and output so a failed
	// verification can never roll the journal's program generation backwards.
	revision := model.RevisionID("")
	fence := uint64(0)
	if output, exists := runtime.Playout.Output(eventID); exists {
		revision = event.Revision
		fence = output.Fence
	}
	record, err := runtime.Journal.Persist(eventID, "source-verification", revision, fence, result)
	if err != nil {
		return verification.Result{}, err
	}
	runtime.VerificationState.AttachJournal(eventID, record.ID)
	result.JournalID = record.ID
	return result, nil
}

// assertSourceIdentity is the on-air gate: a current, passing verification must
// exist and the commit fence must match the verified encoder lease.
func (coordinator *Coordinator) assertSourceIdentity(event model.LiveEvent, fence uint64) error {
	runtime := coordinator.runtime
	expected, ok := runtime.Verification.Expected(event.ID)
	if !ok {
		return fmt.Errorf("event %s has no verified source identity", event.ID)
	}
	if fence != expected.Fence {
		return fmt.Errorf("commit fence %d does not match verified fence %d", fence, expected.Fence)
	}
	return runtime.Verification.CommitAssert(coordinator.observeSource(event, expected))
}
