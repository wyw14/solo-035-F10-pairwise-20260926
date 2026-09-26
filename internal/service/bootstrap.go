package service

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

func Bootstrap(runtime *Runtime) error {
	coordinator := NewCoordinator(runtime)
	event, err := coordinator.CreateEvent("Evening News")
	if err != nil {
		return err
	}
	sourceID := model.NewSourceID()
	status, err := coordinator.PrepareSignal(event.ID, sourceID)
	if err != nil {
		return err
	}
	if err := coordinator.ConfirmSignal(sourceID, status.CurrentCommand); err != nil {
		return err
	}
	base := runtime.Clock().UTC()
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
		if err := coordinator.PushFrame(event.ID, frame); err != nil {
			return err
		}
	}
	if err := coordinator.ConfigureProgram(event.ID, sourceID); err != nil {
		return err
	}
	verified, err := coordinator.VerifySource(event.ID)
	if err != nil {
		return err
	}
	if !verified.Passed {
		return fmt.Errorf("bootstrap source verification failed: %s", verified.Reason)
	}
	if err := coordinator.EstablishInterlocks(event.ID); err != nil {
		return err
	}
	output, ok := runtime.Playout.Output(event.ID)
	if !ok {
		return fmt.Errorf("bootstrap output was not staged")
	}
	if err := coordinator.CommitOnAir(event.ID, output.Fence); err != nil {
		return err
	}
	return nil
}
