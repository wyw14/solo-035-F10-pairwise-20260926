package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func newLineupRuntime(t *testing.T) (*Runtime, *Coordinator) {
	t.Helper()
	runtime, err := NewRuntime(t.TempDir(), func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	return runtime, NewCoordinator(runtime)
}

func pushLineupFrames(t *testing.T, coordinator *Coordinator, eventID model.EventID, sourceID model.SourceID, generation uint64, firstSequence int64, count int) {
	t.Helper()
	base := time.Unix(100, 0).UTC()
	for offset := int64(0); offset < int64(count); offset++ {
		sequence := firstSequence + offset
		frame := model.Frame{
			Source:           sourceID,
			SourceGeneration: generation,
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

func prepareLineup(t *testing.T, runtime *Runtime, coordinator *Coordinator, eventID model.EventID, sourceID model.SourceID) {
	t.Helper()
	status, err := coordinator.PrepareSignal(eventID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ConfirmSignal(sourceID, status.CurrentCommand); err != nil {
		t.Fatal(err)
	}
	pushLineupFrames(t, coordinator, eventID, sourceID, status.Generation, 1, 3)
	if err := coordinator.ConfigureProgram(eventID, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.EstablishInterlocks(eventID); err != nil {
		t.Fatal(err)
	}
}

func recordLineupAudio(t *testing.T, runtime *Runtime, eventID model.EventID) {
	t.Helper()
	waveA, _, err := runtime.AudioMonitor.Measure("probe-a", -18, runtime.Clock())
	if err != nil {
		t.Fatal(err)
	}
	waveB, _, err := runtime.AudioMonitor.Measure("probe-b", -17, runtime.Clock())
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

func sourceCheckRecords(runtime *Runtime, eventID model.EventID) []model.SourceCheck {
	checks := make([]model.SourceCheck, 0)
	for _, record := range runtime.Journal.Records() {
		if record.Kind != "source-check" || record.EventID != eventID {
			continue
		}
		var check model.SourceCheck
		if err := record.Decode(&check); err != nil {
			continue
		}
		checks = append(checks, check)
	}
	return checks
}

func lineupInterlock(runtime *Runtime, eventID model.EventID) model.InterlockView {
	for _, interlock := range runtime.Interlocks() {
		if interlock.EventID == eventID && interlock.Name == "source-lineup" {
			return interlock
		}
	}
	return model.InterlockView{}
}

func TestCommitOnAirRecordsSourceCheck(t *testing.T) {
	runtime, coordinator := newLineupRuntime(t)
	event, err := coordinator.CreateEvent("Late Bulletin")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := model.NewSourceID()
	prepareLineup(t, runtime, coordinator, event.ID, sourceID)
	output, ok := runtime.Playout.Output(event.ID)
	if !ok {
		t.Fatal("output was not staged")
	}
	if err := coordinator.CommitOnAir(event.ID, output.Fence); err != nil {
		t.Fatal(err)
	}
	check, ok := runtime.SourceChecks.Latest(event.ID)
	if !ok || !check.Passed {
		t.Fatalf("latest check = %+v, known = %v", check, ok)
	}
	if check.ExpectedSource != sourceID || check.StagedSource != sourceID || check.ObservedSource != sourceID {
		t.Fatalf("check identities = %+v", check)
	}
	if check.ExpectedGeneration != check.StagedGeneration || check.ExpectedGeneration != check.ObservedGeneration {
		t.Fatalf("check generations = %+v", check)
	}
	if !check.VideoLocked || !check.AudioLocked || !check.FrameKnown {
		t.Fatalf("check lock metadata = %+v", check)
	}
	records := sourceCheckRecords(runtime, event.ID)
	if len(records) != 1 || !records[0].Passed {
		t.Fatalf("journaled source checks = %+v", records)
	}
	if interlock := lineupInterlock(runtime, event.ID); !interlock.Passed {
		t.Fatalf("source-lineup interlock = %+v", interlock)
	}
}

func TestCommitOnAirRejectsMiswiredSource(t *testing.T) {
	runtime, coordinator := newLineupRuntime(t)
	event, err := coordinator.CreateEvent("Late Bulletin")
	if err != nil {
		t.Fatal(err)
	}
	planned := model.NewSourceID()
	status, err := coordinator.PrepareSignal(event.ID, planned)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ConfirmSignal(planned, status.CurrentCommand); err != nil {
		t.Fatal(err)
	}
	pushLineupFrames(t, coordinator, event.ID, planned, status.Generation, 1, 3)
	miswired := model.NewSourceID()
	pushLineupFrames(t, coordinator, event.ID, miswired, 1, 1, 3)
	if err := coordinator.ConfigureProgram(event.ID, planned); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.EstablishInterlocks(event.ID); err != nil {
		t.Fatal(err)
	}
	output, ok := runtime.Playout.Output(event.ID)
	if !ok {
		t.Fatal("output was not staged")
	}
	if !runtime.Sync.Ready(event.ID) {
		t.Fatal("sync window should be stable on the miswired stream")
	}
	err = coordinator.CommitOnAir(event.ID, output.Fence)
	if err == nil || !strings.Contains(err.Error(), "source verification failed") {
		t.Fatalf("commit err = %v", err)
	}
	check, ok := runtime.SourceChecks.Latest(event.ID)
	if !ok || check.Passed {
		t.Fatalf("latest check = %+v, known = %v", check, ok)
	}
	if check.ObservedSource != miswired || check.ExpectedSource != planned {
		t.Fatalf("check identities = %+v", check)
	}
	records := sourceCheckRecords(runtime, event.ID)
	if len(records) != 1 || records[0].Passed {
		t.Fatalf("journaled source checks = %+v", records)
	}
	if got := runtime.Events.GetOrZero(event.ID); got.Phase == model.PhaseOnAir {
		t.Fatal("miswired source went on air")
	}
	if interlock := lineupInterlock(runtime, event.ID); interlock.Passed {
		t.Fatalf("source-lineup interlock = %+v", interlock)
	}
}

func TestSourceChangeRequiresFreshStagingAndCheck(t *testing.T) {
	runtime, coordinator := newLineupRuntime(t)
	event, err := coordinator.CreateEvent("Late Bulletin")
	if err != nil {
		t.Fatal(err)
	}
	first := model.NewSourceID()
	prepareLineup(t, runtime, coordinator, event.ID, first)
	second := model.NewSourceID()
	status, err := coordinator.PrepareSignal(event.ID, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ConfirmSignal(second, status.CurrentCommand); err != nil {
		t.Fatal(err)
	}
	pushLineupFrames(t, coordinator, event.ID, second, status.Generation, 1, 3)
	recordLineupAudio(t, runtime, event.ID)
	output, ok := runtime.Playout.Output(event.ID)
	if !ok {
		t.Fatal("output was not staged")
	}
	err = coordinator.CommitOnAir(event.ID, output.Fence)
	if err == nil || !strings.Contains(err.Error(), "source verification failed") {
		t.Fatalf("commit err = %v", err)
	}
	check, ok := runtime.SourceChecks.Latest(event.ID)
	if !ok || check.Passed || check.StagedSource != first || check.ExpectedSource != second {
		t.Fatalf("latest check = %+v, known = %v", check, ok)
	}
	if err := coordinator.ConfigureProgram(event.ID, second); err != nil {
		t.Fatal(err)
	}
	output, ok = runtime.Playout.Output(event.ID)
	if !ok {
		t.Fatal("output was not restaged")
	}
	if err := coordinator.CommitOnAir(event.ID, output.Fence); err != nil {
		t.Fatal(err)
	}
	records := sourceCheckRecords(runtime, event.ID)
	if len(records) != 2 || records[0].Passed || !records[1].Passed {
		t.Fatalf("journaled source checks = %+v", records)
	}
	if interlock := lineupInterlock(runtime, event.ID); !interlock.Passed {
		t.Fatalf("source-lineup interlock = %+v", interlock)
	}
	if got := runtime.Events.GetOrZero(event.ID); got.Phase != model.PhaseOnAir {
		t.Fatalf("phase = %s", got.Phase)
	}
}

func TestSourceGenerationBumpInvalidatesLastCheck(t *testing.T) {
	runtime, coordinator := newLineupRuntime(t)
	event, err := coordinator.CreateEvent("Late Bulletin")
	if err != nil {
		t.Fatal(err)
	}
	sourceID := model.NewSourceID()
	prepareLineup(t, runtime, coordinator, event.ID, sourceID)
	output, ok := runtime.Playout.Output(event.ID)
	if !ok {
		t.Fatal("output was not staged")
	}
	if err := coordinator.CommitOnAir(event.ID, output.Fence); err != nil {
		t.Fatal(err)
	}
	if interlock := lineupInterlock(runtime, event.ID); !interlock.Passed {
		t.Fatalf("source-lineup interlock = %+v", interlock)
	}
	if _, err := runtime.ExecuteControl(context.Background(), ControlRequest{Action: "advance-source", EventID: string(event.ID)}); err != nil {
		t.Fatal(err)
	}
	interlock := lineupInterlock(runtime, event.ID)
	if interlock.Passed || interlock.Detail != "source plan changed after the last check" {
		t.Fatalf("source-lineup interlock = %+v", interlock)
	}
}
