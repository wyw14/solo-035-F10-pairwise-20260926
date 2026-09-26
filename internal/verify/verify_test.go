package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func lineupFixture() (model.LiveEvent, Observation) {
	source := model.NewSourceID()
	event := model.LiveEvent{
		ID:               model.NewEventID(),
		Revision:         model.NewRevisionID(),
		Source:           source,
		SourceGeneration: 2,
	}
	observation := Observation{
		ObservedSource:     source,
		ObservedGeneration: 2,
		ObservedKnown:      true,
		Frame: model.Frame{
			Source:           source,
			SourceGeneration: 2,
			Sequence:         9,
			Timecode:         1009,
			CapturedAt:       time.Unix(100, 0).UTC(),
		},
		FrameKnown: true,
		Signal: model.SignalStatus{
			Source:      source,
			Generation:  2,
			VideoLocked: true,
			AudioLocked: true,
		},
		SignalKnown:      true,
		StagedSource:     source,
		StagedGeneration: 2,
		StagedFence:      7,
		StagedKnown:      true,
	}
	return event, observation
}

func TestEvaluateMatchesPreparedLineup(t *testing.T) {
	event, observation := lineupFixture()
	check := Evaluate(event, 7, observation, time.Unix(200, 0))
	if !check.Passed {
		t.Fatalf("check failed: %s", check.Reason)
	}
	if check.ExpectedSource != event.Source || check.ExpectedGeneration != 2 {
		t.Fatalf("expected identity not recorded: %+v", check)
	}
	if check.FrameSequence != 9 || check.FrameTimecode != 1009 || !check.FrameKnown {
		t.Fatalf("frame metadata not recorded: %+v", check)
	}
	if check.StagedFence != 7 || check.CommitFence != 7 || !check.VideoLocked || !check.AudioLocked {
		t.Fatalf("staged or lock metadata not recorded: %+v", check)
	}
	if check.CheckedAt != time.Unix(200, 0).UTC() {
		t.Fatalf("checked_at = %s", check.CheckedAt)
	}
}

func TestEvaluateRejectsLineupDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(event *model.LiveEvent, observation *Observation)
		reason string
	}{
		{"expected source not recorded", func(event *model.LiveEvent, observation *Observation) {
			event.Source = ""
		}, "not recorded"},
		{"no staged output", func(event *model.LiveEvent, observation *Observation) {
			observation.StagedKnown = false
		}, "no staged playout output"},
		{"staged source differs", func(event *model.LiveEvent, observation *Observation) {
			observation.StagedSource = model.NewSourceID()
		}, "different source"},
		{"staged generation stale", func(event *model.LiveEvent, observation *Observation) {
			observation.StagedGeneration = 1
		}, "stale"},
		{"commit fence mismatch", func(event *model.LiveEvent, observation *Observation) {
			observation.StagedFence = 6
		}, "fence"},
		{"signal missing", func(event *model.LiveEvent, observation *Observation) {
			observation.SignalKnown = false
		}, "no registered signal"},
		{"signal generation mismatch", func(event *model.LiveEvent, observation *Observation) {
			observation.Signal.Generation = 3
		}, "planned generation"},
		{"signal not locked", func(event *model.LiveEvent, observation *Observation) {
			observation.Signal.VideoLocked = false
		}, "not fully locked"},
		{"no received frame", func(event *model.LiveEvent, observation *Observation) {
			observation.FrameKnown = false
		}, "no frame"},
		{"frame generation mismatch", func(event *model.LiveEvent, observation *Observation) {
			observation.Frame.SourceGeneration = 1
		}, "another source generation"},
		{"observed source differs", func(event *model.LiveEvent, observation *Observation) {
			observation.ObservedSource = model.NewSourceID()
		}, "different source than planned"},
		{"observed generation differs", func(event *model.LiveEvent, observation *Observation) {
			observation.ObservedGeneration = 3
		}, "different source generation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, observation := lineupFixture()
			tc.mutate(&event, &observation)
			check := Evaluate(event, 7, observation, time.Unix(200, 0))
			if check.Passed {
				t.Fatalf("check passed unexpectedly: %+v", check)
			}
			if !strings.Contains(check.Reason, tc.reason) {
				t.Fatalf("reason %q does not mention %q", check.Reason, tc.reason)
			}
		})
	}
}

func TestStateRetainsLatestCheckPerEvent(t *testing.T) {
	state := NewState()
	event, observation := lineupFixture()
	first := Evaluate(event, 7, observation, time.Unix(200, 0))
	observation.StagedSource = model.NewSourceID()
	second := Evaluate(event, 7, observation, time.Unix(300, 0))
	state.Record(first)
	state.Record(second)
	latest, ok := state.Latest(event.ID)
	if !ok {
		t.Fatal("no check recorded")
	}
	if latest.Passed || latest.CheckedAt != time.Unix(300, 0).UTC() {
		t.Fatalf("latest = %+v", latest)
	}
	if _, ok := state.Latest(model.NewEventID()); ok {
		t.Fatal("unknown event reported a check")
	}
}
