package verification

import (
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func fixedClock() func() time.Time {
	base := time.Unix(1000, 0).UTC()
	return func() time.Time { return base }
}

func fixture() (Expected, Observation) {
	expected := Expected{
		EventID:    model.NewEventID(),
		Source:     model.NewSourceID(),
		Generation: 2,
		Revision:   model.NewRevisionID(),
		Fence:      7,
		PlannedAt:  time.Unix(1000, 0).UTC(),
	}
	obs := Observation{
		EventID:               expected.EventID,
		EventRevision:         expected.Revision,
		EventSourceGeneration: expected.Generation,
		Frame: FrameEvidence{
			Present:    true,
			Source:     expected.Source,
			Generation: expected.Generation,
			Sequence:   42,
			Timecode:   5000,
			CapturedAt: time.Unix(200, 0).UTC(),
		},
		Lock: LockEvidence{
			Present:        true,
			Source:         expected.Source,
			Generation:     expected.Generation,
			CurrentCommand: model.NewCommandID(),
			VideoLocked:    true,
			AudioLocked:    true,
		},
		Output: OutputEvidence{
			Present:  true,
			EventID:  expected.EventID,
			Source:   expected.Source,
			Revision: expected.Revision,
			Fence:    expected.Fence,
		},
	}
	return expected, obs
}

func TestEvaluatePassesWithMatchingMetadata(t *testing.T) {
	expected, obs := fixture()
	result := Evaluate(expected, obs, time.Unix(300, 0))
	if !result.Passed {
		t.Fatalf("expected verification to pass, reason: %s", result.Reason)
	}
	if result.Reason != confirmedReason {
		t.Fatalf("reason = %q", result.Reason)
	}
	if result.Fingerprint != expected.Fingerprint() {
		t.Fatal("result fingerprint must identify the plan")
	}
}

func TestEvaluateRejectsMismatchedMetadata(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Observation)
		want   string
	}{
		{"missing output", func(o *Observation) { o.Output = OutputEvidence{} }, "no playout output"},
		{"wrong output source", func(o *Observation) { o.Output.Source = model.NewSourceID() }, "output source"},
		{"wrong output revision", func(o *Observation) { o.Output.Revision = model.NewRevisionID() }, "output revision"},
		{"wrong output fence", func(o *Observation) { o.Output.Fence = 99 }, "output fence"},
		{"no recent frame", func(o *Observation) { o.Frame = FrameEvidence{} }, "no recent frame"},
		{"frame from another camera", func(o *Observation) { o.Frame.Source = model.NewSourceID() }, "another camera"},
		{"frame from another generation", func(o *Observation) { o.Frame.Generation = 3 }, "another source generation"},
		{"missing lock", func(o *Observation) { o.Lock = LockEvidence{} }, "lock status"},
		{"lock generation drift", func(o *Observation) { o.Lock.Generation = 3 }, "lock belongs"},
		{"media lock lost", func(o *Observation) { o.Lock.AudioLocked = false }, "complete media lock"},
		{"program revision advanced", func(o *Observation) { o.EventRevision = model.NewRevisionID() }, "revision advanced"},
		{"source generation advanced", func(o *Observation) { o.EventSourceGeneration = 3 }, "generation advanced"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected, obs := fixture()
			tc.mutate(&obs)
			result := Evaluate(expected, obs, time.Unix(300, 0))
			if result.Passed {
				t.Fatalf("expected failure for %s", tc.name)
			}
			if !strings.Contains(result.Reason, tc.want) {
				t.Fatalf("reason = %q, want substring %q", result.Reason, tc.want)
			}
		})
	}
}

func TestPlanChangeClearsLatestConfirmation(t *testing.T) {
	expected, obs := fixture()
	coordinator := NewCoordinator(NewState(), fixedClock())
	if _, err := coordinator.PlanSource(expected.EventID, expected.Source, expected.Generation, expected.Revision, expected.Fence); err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Verify(obs)
	if err != nil || !result.Passed {
		t.Fatalf("initial verify failed: %v %s", err, result.Reason)
	}
	if err := coordinator.CommitAssert(obs); err != nil {
		t.Fatalf("commit should be allowed: %v", err)
	}

	// A replan records a new plan and the old confirmation must no longer gate.
	replanned := expected
	replanned.Generation = 3
	if _, err := coordinator.PlanSource(replanned.EventID, replanned.Source, replanned.Generation, replanned.Revision, replanned.Fence); err != nil {
		t.Fatal(err)
	}
	if _, ok := coordinator.Latest(expected.EventID); ok {
		t.Fatal("replan must clear the latest result")
	}
	staleObs := obs
	staleObs.EventSourceGeneration = 3
	if err := coordinator.CommitAssert(staleObs); err == nil {
		t.Fatal("commit without a fresh verification must be denied")
	}
}

func TestInvalidationRemovesPlan(t *testing.T) {
	expected, obs := fixture()
	coordinator := NewCoordinator(NewState(), fixedClock())
	if _, err := coordinator.PlanSource(expected.EventID, expected.Source, expected.Generation, expected.Revision, expected.Fence); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Verify(obs); err != nil {
		t.Fatal(err)
	}
	coordinator.Invalidate(expected.EventID)
	if _, ok := coordinator.Expected(expected.EventID); ok {
		t.Fatal("invalidate must remove the plan")
	}
	if err := coordinator.CommitAssert(obs); err == nil {
		t.Fatal("commit after invalidation must be denied")
	}
	if history := coordinator.History(expected.EventID); len(history) != 1 {
		t.Fatalf("history should retain evidence, got %d", len(history))
	}
}

func TestInvalidateSourceMatchesPlannedSource(t *testing.T) {
	expected, obs := fixture()
	coordinator := NewCoordinator(NewState(), fixedClock())
	if _, err := coordinator.PlanSource(expected.EventID, expected.Source, expected.Generation, expected.Revision, expected.Fence); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Verify(obs); err != nil {
		t.Fatal(err)
	}
	coordinator.InvalidateSource(model.NewSourceID())
	if _, ok := coordinator.Expected(expected.EventID); !ok {
		t.Fatal("unrelated source retirement must not invalidate the plan")
	}
	coordinator.InvalidateSource(expected.Source)
	if _, ok := coordinator.Expected(expected.EventID); ok {
		t.Fatal("retiring the planned source must invalidate the plan")
	}
}

func TestHistoryIsBounded(t *testing.T) {
	expected, obs := fixture()
	state := NewState()
	if err := state.Plan(expected); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < historyLimit+5; i++ {
		state.Save(Evaluate(expected, obs, time.Unix(int64(300+i), 0)))
	}
	history := state.History(expected.EventID)
	if len(history) != historyLimit {
		t.Fatalf("history = %d, want %d", len(history), historyLimit)
	}
}

func TestRecoveryRoundTrip(t *testing.T) {
	expected, obs := fixture()
	state := NewState()
	coordinator := NewCoordinator(state, fixedClock())
	if _, err := coordinator.PlanSource(expected.EventID, expected.Source, expected.Generation, expected.Revision, expected.Fence); err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Verify(obs)
	if err != nil {
		t.Fatal(err)
	}
	records := state.ExportRecovery()
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}

	restored := NewState()
	if err := restored.RestoreRecovery(records); err != nil {
		t.Fatal(err)
	}
	gotExpected, ok := restored.Expected(expected.EventID)
	if !ok || gotExpected != expected {
		t.Fatal("expected identity was not restored")
	}
	latest, ok := restored.Latest(expected.EventID)
	if !ok || latest.Passed != result.Passed || latest.Fingerprint != expected.Fingerprint() {
		t.Fatal("latest verification was not restored")
	}
}
