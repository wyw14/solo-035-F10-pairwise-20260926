package service

import (
	"context"
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/caption"
	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/verify"
)

type Coordinator struct {
	runtime *Runtime
}

func NewCoordinator(runtime *Runtime) *Coordinator {
	return &Coordinator{runtime: runtime}
}

func (coordinator *Coordinator) CreateEvent(name string) (model.LiveEvent, error) {
	event, err := coordinator.runtime.Events.Create(name, coordinator.runtime.Clock())
	if err != nil {
		return model.LiveEvent{}, err
	}
	if _, err := coordinator.runtime.EventFlow.Arm(event.ID); err != nil {
		return model.LiveEvent{}, err
	}
	return coordinator.runtime.Events.GetOrZero(event.ID), nil
}

func (coordinator *Coordinator) PrepareSignal(eventID model.EventID, sourceID model.SourceID) (model.SignalStatus, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return model.SignalStatus{}, fmt.Errorf("event %s not found", eventID)
	}
	status := coordinator.runtime.SourceFlow.Switch(sourceID)
	event = event.NewSource(coordinator.runtime.Clock())
	event.Source = sourceID
	event.SourceGeneration = status.Generation
	if err := coordinator.runtime.Events.Update(event); err != nil {
		return model.SignalStatus{}, err
	}
	coordinator.runtime.Quality.Reset(eventID)
	if err := coordinator.runtime.Sync.Open(eventID, 3); err != nil {
		return model.SignalStatus{}, err
	}
	return status, nil
}

func (coordinator *Coordinator) ConfirmSignal(sourceID model.SourceID, command model.CommandID) error {
	if err := coordinator.runtime.SourceFlow.Confirm(sourceID, command, true, true); err != nil {
		return err
	}
	if !coordinator.runtime.SourceFlow.Ready(sourceID) {
		return fmt.Errorf("source %s did not retain its current lock", sourceID)
	}
	return nil
}

func (coordinator *Coordinator) PushFrame(eventID model.EventID, frame model.Frame) error {
	received, err := coordinator.runtime.Receiver.Push(frame.Source, frame.SourceGeneration, frame.Sequence, frame.Timecode, frame.CapturedAt)
	if err != nil {
		return err
	}
	received.VideoLocked = frame.VideoLocked
	received.AudioLocked = frame.AudioLocked
	_, err = coordinator.runtime.Sync.Observe(eventID, received)
	return err
}

func (coordinator *Coordinator) ConfigureProgram(eventID model.EventID, sourceID model.SourceID) error {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return fmt.Errorf("event %s not found", eventID)
	}
	lease, err := coordinator.runtime.EncoderFlow.ReservePrimary(eventID, "1080p50")
	if err != nil {
		return err
	}
	if err := coordinator.runtime.EncoderFlow.Start(lease); err != nil {
		return err
	}
	program, err := coordinator.runtime.Mux.Configure(event, lease, 8000)
	if err != nil {
		return err
	}
	if err := coordinator.runtime.Mux.Activate(eventID, program.Revision); err != nil {
		return err
	}
	return coordinator.runtime.Playout.Stage(event, sourceID, event.SourceGeneration, lease.Fence, "current-program")
}

func (coordinator *Coordinator) EstablishInterlocks(eventID model.EventID) error {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return fmt.Errorf("event %s not found", eventID)
	}
	waveA, _, err := coordinator.runtime.AudioMonitor.Measure("probe-a", -18, coordinator.runtime.Clock())
	if err != nil {
		return err
	}
	waveB, _, err := coordinator.runtime.AudioMonitor.Measure("probe-b", -17, coordinator.runtime.Clock())
	if err != nil {
		return err
	}
	if err := coordinator.runtime.Evaluator.Accept(waveA); err != nil {
		return err
	}
	if err := coordinator.runtime.Evaluator.Accept(waveB); err != nil {
		return err
	}
	if err := coordinator.runtime.Quality.Record(eventID, waveA); err != nil {
		return err
	}
	if err := coordinator.runtime.Quality.Record(eventID, waveB); err != nil {
		return err
	}
	if err := coordinator.runtime.Continuity.Accept(eventID, caption.Cue{Revision: event.Revision, Sequence: 1, Text: "ON AIR"}); err != nil {
		return err
	}
	coordinator.runtime.CaptionFlow.Encode(eventID, event.Revision, "ON AIR")
	return nil
}

func (coordinator *Coordinator) VerifySource(eventID model.EventID, fence uint64) (model.SourceCheck, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return model.SourceCheck{}, fmt.Errorf("event %s not found", eventID)
	}
	observation := verify.Observation{}
	if status, ok := coordinator.runtime.Sync.Status(eventID); ok {
		observation.ObservedSource = status.Source
		observation.ObservedGeneration = status.SourceGeneration
		observation.ObservedKnown = true
	}
	if event.Source.Valid() {
		if frame, ok := coordinator.runtime.Sources.LatestFrame(event.Source); ok {
			observation.Frame = frame
			observation.FrameKnown = true
		}
		if signal, ok := coordinator.runtime.Sources.Status(event.Source); ok {
			observation.Signal = signal
			observation.SignalKnown = true
		}
	}
	if output, ok := coordinator.runtime.Playout.Output(eventID); ok {
		observation.StagedSource = output.Source
		observation.StagedGeneration = output.SourceGeneration
		observation.StagedFence = output.Fence
		observation.StagedKnown = true
	}
	check := verify.Evaluate(event, fence, observation, coordinator.runtime.Clock())
	coordinator.runtime.SourceChecks.Record(check)
	if _, err := coordinator.runtime.Journal.Persist(event.ID, "source-check", event.Revision, fence, check); err != nil {
		return check, fmt.Errorf("source check was not durable: %w", err)
	}
	if !check.Passed {
		return check, fmt.Errorf("source verification failed: %s", check.Reason)
	}
	return check, nil
}

func (coordinator *Coordinator) CommitOnAir(eventID model.EventID, fence uint64) error {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return fmt.Errorf("event %s not found", eventID)
	}
	if !coordinator.runtime.Sync.Ready(eventID) {
		return fmt.Errorf("frame synchronization is not stable")
	}
	if err := coordinator.runtime.CaptionFlow.Ready(eventID, event.Revision); err != nil {
		return err
	}
	if _, err := coordinator.runtime.Quality.Permit(eventID, true, true); err != nil {
		return err
	}
	if _, err := coordinator.VerifySource(eventID, fence); err != nil {
		return err
	}
	if err := coordinator.runtime.Playout.CommitOnAir(event, fence, map[string]any{"offset_ms": 0, "source_generation": event.SourceGeneration}); err != nil {
		return err
	}
	_, err := coordinator.runtime.EventFlow.MarkOnAir(eventID)
	return err
}

func (coordinator *Coordinator) ScheduleFailover(ctx context.Context, eventID model.EventID, delay time.Duration) (<-chan error, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return nil, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Backup.Schedule(ctx, event, "1080p50-backup", delay), nil
}

func (coordinator *Coordinator) CancelFailover(eventID model.EventID) error {
	if _, err := coordinator.runtime.EventFlow.CancelCommand(eventID); err != nil {
		return err
	}
	return coordinator.runtime.Backup.Cancel(eventID)
}
