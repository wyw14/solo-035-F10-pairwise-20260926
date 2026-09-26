package verify

import (
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

type Observation struct {
	ObservedSource     model.SourceID
	ObservedGeneration uint64
	ObservedKnown      bool
	Frame              model.Frame
	FrameKnown         bool
	Signal             model.SignalStatus
	SignalKnown        bool
	StagedSource       model.SourceID
	StagedGeneration   uint64
	StagedFence        uint64
	StagedKnown        bool
}

func Evaluate(event model.LiveEvent, commitFence uint64, observation Observation, now time.Time) model.SourceCheck {
	check := model.SourceCheck{
		EventID:            event.ID,
		Revision:           event.Revision,
		ExpectedSource:     event.Source,
		ExpectedGeneration: event.SourceGeneration,
		ObservedSource:     observation.ObservedSource,
		ObservedGeneration: observation.ObservedGeneration,
		ObservedKnown:      observation.ObservedKnown,
		SignalKnown:        observation.SignalKnown,
		SignalGeneration:   observation.Signal.Generation,
		VideoLocked:        observation.Signal.VideoLocked,
		AudioLocked:        observation.Signal.AudioLocked,
		StagedKnown:        observation.StagedKnown,
		StagedSource:       observation.StagedSource,
		StagedGeneration:   observation.StagedGeneration,
		StagedFence:        observation.StagedFence,
		CommitFence:        commitFence,
		CheckedAt:          now.UTC(),
	}
	if observation.FrameKnown {
		check.FrameKnown = true
		check.FrameSequence = observation.Frame.Sequence
		check.FrameTimecode = observation.Frame.Timecode
		check.FrameCapturedAt = observation.Frame.CapturedAt.UTC()
	}
	check.Passed, check.Reason = verdict(event, commitFence, observation)
	return check
}

func verdict(event model.LiveEvent, commitFence uint64, observation Observation) (bool, string) {
	switch {
	case !event.Source.Valid() || event.SourceGeneration == 0:
		return false, "expected source identity was not recorded during preparation"
	case !observation.StagedKnown:
		return false, "no staged playout output to verify against"
	case observation.StagedSource != event.Source:
		return false, "staged output carries a different source than planned"
	case observation.StagedGeneration != event.SourceGeneration:
		return false, "staged output source generation is stale"
	case observation.StagedFence != commitFence:
		return false, "staged output fence does not match the commit fence"
	case !observation.SignalKnown:
		return false, "expected source has no registered signal"
	case observation.Signal.Generation != event.SourceGeneration:
		return false, "locked signal generation does not match the planned generation"
	case !observation.Signal.VideoLocked || !observation.Signal.AudioLocked:
		return false, "expected source is not fully locked"
	case !observation.FrameKnown:
		return false, "receiver has no frame from the expected source"
	case observation.Frame.Source != event.Source:
		return false, "latest received frame carries a different source than planned"
	case observation.Frame.SourceGeneration != event.SourceGeneration:
		return false, "latest received frame belongs to another source generation"
	case observation.ObservedKnown && observation.ObservedSource != event.Source:
		return false, "incoming stream carries a different source than planned"
	case observation.ObservedKnown && observation.ObservedGeneration != event.SourceGeneration:
		return false, "incoming stream carries a different source generation than planned"
	default:
		return true, "received source matches the prepared output"
	}
}
