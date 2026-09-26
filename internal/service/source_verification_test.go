package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/verification"
)

func bootstrappedRuntime(t *testing.T) (*Runtime, *Coordinator) {
	t.Helper()
	base := time.Unix(100, 0).UTC()
	runtime, err := NewRuntime(t.TempDir(), func() time.Time { return base })
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	return runtime, NewCoordinator(runtime)
}

func plannedSource(t *testing.T, runtime *Runtime) (model.EventID, model.SourceID, uint64) {
	t.Helper()
	events := runtime.Events.List()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	output, ok := runtime.Playout.Output(events[0].ID)
	if !ok {
		t.Fatal("expected a staged playout output")
	}
	return events[0].ID, output.Source, output.Fence
}

// switchSource prepares a receiver for sourceID and feeds stable frames to the
// event through the coordinator, exactly like the real ingestion path.
func switchSource(t *testing.T, runtime *Runtime, coordinator *Coordinator, eventID model.EventID, sourceID model.SourceID, base time.Time) {
	t.Helper()
	status, err := coordinator.PrepareSignal(eventID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ConfirmSignal(sourceID, status.CurrentCommand); err != nil {
		t.Fatal(err)
	}
	for sequence := int64(1); sequence <= 3; sequence++ {
		frame := model.Frame{
			Source:           sourceID,
			SourceGeneration: status.Generation,
			Sequence:         sequence,
			Timecode:         1000 + sequence,
			CapturedAt:       base.Add(time.Duration(sequence) * 20 * time.Millisecond),
			VideoLocked:      true,
			AudioLocked:      true,
		}
		if err := coordinator.PushFrame(eventID, frame); err != nil {
			t.Fatal(err)
		}
	}
}

// feedUnexpectedFrames pushes frames from a source the event never prepared.
// The receiver auto-registers it (as it would for a misconnected camera),
// without invalidating or replacing the recorded source plan.
func feedUnexpectedFrames(t *testing.T, runtime *Runtime, coordinator *Coordinator, eventID model.EventID, sourceID model.SourceID, base time.Time) {
	t.Helper()
	for sequence := int64(1); sequence <= 3; sequence++ {
		frame := model.Frame{
			Source:           sourceID,
			SourceGeneration: 1,
			Sequence:         sequence,
			Timecode:         1000 + sequence,
			CapturedAt:       base.Add(time.Duration(sequence) * 20 * time.Millisecond),
			VideoLocked:      true,
			AudioLocked:      true,
		}
		if err := coordinator.PushFrame(eventID, frame); err != nil {
			t.Fatal(err)
		}
	}
}

func replenishQuality(t *testing.T, runtime *Runtime, eventID model.EventID) {
	t.Helper()
	now := runtime.Clock()
	waveA, _, err := runtime.AudioMonitor.Measure("probe-a", -18, now)
	if err != nil {
		t.Fatal(err)
	}
	waveB, _, err := runtime.AudioMonitor.Measure("probe-b", -17, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Evaluator.Accept(waveA); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Evaluator.Accept(waveB); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Quality.Record(eventID, waveA); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Quality.Record(eventID, waveB); err != nil {
		t.Fatal(err)
	}
}

func TestSourceIdentityInterlockPassesAfterBootstrap(t *testing.T) {
	runtime, _ := bootstrappedRuntime(t)
	var sourceIdentity *model.InterlockView
	for i := range runtime.Snapshot().Interlocks {
		if runtime.Snapshot().Interlocks[i].Name == "source-identity" {
			sourceIdentity = &runtime.Snapshot().Interlocks[i]
		}
	}
	if sourceIdentity == nil {
		t.Fatal("snapshot has no source-identity interlock")
	}
	if !sourceIdentity.Passed {
		t.Fatalf("source-identity interlock failed: %s", sourceIdentity.Detail)
	}
	if !runtime.Snapshot().Healthy() {
		t.Fatal("bootstrapped runtime must stay healthy")
	}
}

func TestWrongCameraIsRejected(t *testing.T) {
	runtime, coordinator := bootstrappedRuntime(t)
	eventID, planned, fence := plannedSource(t, runtime)
	base := runtime.Clock()

	// A different camera starts arriving at the receiver without any planned
	// switch: the prepared output and plan still point at the planned source.
	other := model.NewSourceID()
	feedUnexpectedFrames(t, runtime, coordinator, eventID, other, base.Add(time.Minute))

	result, err := coordinator.VerifySource(eventID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Fatal("verification must fail when the most recent frame comes from another camera")
	}
	if !strings.Contains(result.Reason, "another camera") {
		t.Fatalf("reason = %q", result.Reason)
	}
	if result.Observed.Frame.Source != other || result.Observed.Output.Source != planned {
		t.Fatal("evidence must record both the observed and the planned source")
	}
	if err := coordinator.CommitOnAir(eventID, fence); err == nil {
		t.Fatal("commit must be denied while the wrong camera is connected")
	}
}

func holdEvent(t *testing.T, runtime *Runtime, eventID model.EventID) {
	t.Helper()
	if _, err := runtime.ExecuteControl(t.Context(), ControlRequest{Action: "hold-event", EventID: string(eventID)}); err != nil {
		t.Fatal(err)
	}
}

func TestSourceSwitchInvalidatesEarlierConfirmation(t *testing.T) {
	runtime, coordinator := bootstrappedRuntime(t)
	eventID, source, fence := plannedSource(t, runtime)
	base := runtime.Clock()

	// Re-preparing the same camera bumps its generation; the earlier pass must
	// no longer authorize the commit even before any B frame arrives.
	switchSource(t, runtime, coordinator, eventID, source, base.Add(2*time.Minute))
	if result, err := coordinator.VerifySource(eventID); err != nil {
		t.Fatal(err)
	} else if result.Passed {
		t.Fatal("verification must fail: the output still references the old generation")
	}
	if err := coordinator.CommitOnAir(eventID, fence); err == nil {
		t.Fatal("commit must be denied after a source generation change")
	}

	// Replan to the freshly prepared generation and verify again.
	req := ControlRequest{Action: "replan-source", EventID: string(eventID), SourceID: string(source)}
	control, err := runtime.ExecuteControl(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	planned, ok := control.Value.(verification.Expected)
	if !ok || planned.Generation < 2 {
		t.Fatalf("replanned identity = %#v", control.Value)
	}
	result, err := coordinator.VerifySource(eventID)
	if err != nil || !result.Passed {
		t.Fatalf("re-verification after replan must pass: %v %s", err, result.Reason)
	}
	replenishQuality(t, runtime, eventID)
	holdEvent(t, runtime, eventID)
	if err := coordinator.CommitOnAir(eventID, fence); err != nil {
		t.Fatalf("commit after replan and re-verification must pass: %v", err)
	}
	output, _ := runtime.Playout.Output(eventID)
	if !output.OnAir {
		t.Fatal("output should be on air after a successful commit")
	}
}

func TestAdvanceSourceBypassesReceiverAndStillLocksCommit(t *testing.T) {
	runtime, coordinator := bootstrappedRuntime(t)
	eventID, _, fence := plannedSource(t, runtime)

	if _, err := runtime.ExecuteControl(t.Context(), ControlRequest{Action: "advance-source", EventID: string(eventID)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Verification.Expected(eventID); ok {
		t.Fatal("advance-source must invalidate the recorded source plan")
	}
	if err := coordinator.CommitOnAir(eventID, fence); err == nil {
		t.Fatal("commit must be denied after a source generation advance")
	}
}

func TestReplanToOtherCameraKeepsFenceAndRequiresReverify(t *testing.T) {
	runtime, coordinator := bootstrappedRuntime(t)
	eventID, original, fence := plannedSource(t, runtime)
	base := runtime.Clock()
	before, _ := runtime.Verification.Latest(eventID)
	other := model.NewSourceID()
	switchSource(t, runtime, coordinator, eventID, other, base.Add(3*time.Minute))

	req := ControlRequest{Action: "replan-source", EventID: string(eventID), SourceID: string(other)}
	if _, err := runtime.ExecuteControl(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Verification.Latest(eventID); ok {
		t.Fatal("replan must clear the earlier verification result")
	}
	output, _ := runtime.Playout.Output(eventID)
	if output.Source != other || output.Fence != fence || output.Source == original {
		t.Fatal("replan must repoint the output to the new source while keeping the fence")
	}
	if trace := runtime.Playout.Trace(eventID); !contains(trace, "staged:replanned-source") {
		t.Fatalf("trace = %v", trace)
	}
	if err := coordinator.CommitOnAir(eventID, fence); err == nil {
		t.Fatal("commit must require a fresh verification after replan")
	}
	result, err := coordinator.VerifySource(eventID)
	if err != nil || !result.Passed {
		t.Fatalf("verification against the replanned camera must pass: %v %s", err, result.Reason)
	}
	replenishQuality(t, runtime, eventID)
	holdEvent(t, runtime, eventID)
	if err := coordinator.CommitOnAir(eventID, fence); err != nil {
		t.Fatalf("commit after replan verification must pass: %v", err)
	}
	if before.JournalID == "" {
		t.Fatal("bootstrap verification should carry a journal record id")
	}
}

func TestReplanToUnregisteredSourceIsRejected(t *testing.T) {
	runtime, _ := bootstrappedRuntime(t)
	eventID, _, _ := plannedSource(t, runtime)
	req := ControlRequest{Action: "replan-source", EventID: string(eventID), SourceID: string(model.NewSourceID())}
	if _, err := runtime.ExecuteControl(t.Context(), req); err == nil {
		t.Fatal("replan to a source with no receiver registration must fail")
	}
}

func TestVerificationAttemptsAreJournaledWithMetadata(t *testing.T) {
	runtime, coordinator := bootstrappedRuntime(t)
	eventID, _, _ := plannedSource(t, runtime)
	base := runtime.Clock()
	other := model.NewSourceID()
	feedUnexpectedFrames(t, runtime, coordinator, eventID, other, base.Add(4*time.Minute))
	if _, err := coordinator.VerifySource(eventID); err != nil {
		t.Fatal(err)
	}

	var passed, failed int
	var failedPayload map[string]any
	for _, record := range runtime.Journal.Records() {
		if record.Kind != "source-verification" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["passed"] == true {
			passed++
		} else {
			failed++
			failedPayload = payload
		}
	}
	if passed == 0 || failed == 0 {
		t.Fatalf("expected both passing and failing evidence, got passed=%d failed=%d", passed, failed)
	}
	observed, ok := failedPayload["observed"].(map[string]any)
	if !ok {
		t.Fatal("failed evidence is missing observed metadata")
	}
	for _, key := range []string{"frame", "lock", "output", "event_revision", "event_source_generation"} {
		if _, ok := observed[key]; !ok {
			t.Fatalf("evidence is missing %s", key)
		}
	}
	frame := observed["frame"].(map[string]any)
	for _, key := range []string{"source", "generation", "sequence", "timecode"} {
		if _, ok := frame[key]; !ok {
			t.Fatalf("frame evidence is missing %s", key)
		}
	}
	lock := observed["lock"].(map[string]any)
	for _, key := range []string{"current_command", "video_locked", "audio_locked"} {
		if _, ok := lock[key]; !ok {
			t.Fatalf("lock evidence is missing %s", key)
		}
	}
	output := observed["output"].(map[string]any)
	for _, key := range []string{"source", "revision", "fence"} {
		if _, ok := output[key]; !ok {
			t.Fatalf("output evidence is missing %s", key)
		}
	}
	if _, ok := failedPayload["checked_at"]; !ok {
		t.Fatal("evidence is missing checked_at")
	}
}

func TestFailedVerificationKeepsJournalDiagnosticsHealthy(t *testing.T) {
	runtime, coordinator := bootstrappedRuntime(t)
	eventID, _, _ := plannedSource(t, runtime)
	other := model.NewSourceID()
	feedUnexpectedFrames(t, runtime, coordinator, eventID, other, runtime.Clock().Add(5*time.Minute))
	if _, err := coordinator.VerifySource(eventID); err != nil {
		t.Fatal(err)
	}
	for _, check := range runtime.Diagnostics().Checks {
		if check.Name == "journal-order" && !check.Passed {
			t.Fatalf("journal diagnostics must accept verification records: %s", check.Detail)
		}
	}
}

func TestCheckpointRestoresVerificationPlan(t *testing.T) {
	runtime, _ := bootstrappedRuntime(t)
	eventID, source, _ := plannedSource(t, runtime)
	checkpoint := runtime.ExportCheckpoint()
	if len(checkpoint.Verification) != 1 {
		t.Fatalf("verification records = %d", len(checkpoint.Verification))
	}

	restored, err := NewRuntime(t.TempDir(), runtime.Clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.RestoreCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	expected, ok := restored.Verification.Expected(eventID)
	if !ok {
		t.Fatal("restored runtime must retain the planned source identity")
	}
	if expected.Source != source {
		t.Fatal("restored source does not match the checkpoint")
	}
	latest, ok := restored.Verification.Latest(eventID)
	if !ok || !latest.Passed {
		t.Fatal("restored runtime must retain the latest passing verification")
	}
	// No receiver frames or locks exist in the restored runtime, so the live
	// observation can never satisfy the on-air gate.
	if err := NewCoordinator(restored).CommitOnAir(eventID, expected.Fence); err == nil {
		t.Fatal("commit without live receiver evidence must stay locked after restore")
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
