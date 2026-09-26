package service

import (
	"sort"

	"github.com/google/uuid"
	"github.com/wyw14/solo-035/internal/model"
)

func (runtime *Runtime) Snapshot() model.RuntimeSnapshot {
	snapshot := model.RuntimeSnapshot{CapturedAt: runtime.Clock().UTC()}
	for _, event := range runtime.Events.List() {
		snapshot.Operations = append(snapshot.Operations, model.OperationView{
			EventID:          event.ID,
			Name:             event.Name,
			Phase:            event.Phase,
			ProgramRevision:  event.Revision,
			SourceGeneration: event.SourceGeneration,
			CommandEpoch:     event.CommandEpoch,
		})
		status, synced := runtime.Sync.Status(event.ID)
		snapshot.Interlocks = append(snapshot.Interlocks, model.InterlockView{EventID: event.ID, Name: "frame-sync", Passed: synced && status.Stable, Detail: status.Reason})
		captionStatus, captionKnown := runtime.Captions.Get(event.ID)
		captionPassed := captionKnown && captionStatus.Continuous && captionStatus.EncoderErr == ""
		snapshot.Interlocks = append(snapshot.Interlocks, model.InterlockView{EventID: event.ID, Name: "caption-continuity", Passed: captionPassed, Detail: captionStatus.EncoderErr})
		snapshot.Interlocks = append(snapshot.Interlocks, runtime.sourceIdentityInterlock(event.ID))
		if captionStatus.EncoderErr != "" {
			snapshot.Incidents = append(snapshot.Incidents, model.IncidentView{ID: uuid.NewString(), EventID: event.ID, Severity: "error", Message: captionStatus.EncoderErr, CreatedAt: captionStatus.UpdatedAt})
		}
	}
	for _, resource := range runtime.EncoderFlow.Equipment() {
		snapshot.Equipment = append(snapshot.Equipment, model.EquipmentView{ID: resource.ID, Kind: "encoder", Owner: resource.Lease.Owner, Fence: resource.Lease.Fence, Healthy: resource.Healthy})
	}
	for _, source := range runtime.SourceFlow.Snapshot() {
		snapshot.Equipment = append(snapshot.Equipment, model.EquipmentView{ID: string(source.Source), Kind: "source", Fence: source.Generation, Healthy: source.VideoLocked && source.AudioLocked})
	}
	sort.Slice(snapshot.Operations, func(i, j int) bool { return snapshot.Operations[i].Name < snapshot.Operations[j].Name })
	sort.Slice(snapshot.Equipment, func(i, j int) bool { return snapshot.Equipment[i].ID < snapshot.Equipment[j].ID })
	sort.Slice(snapshot.Interlocks, func(i, j int) bool {
		if snapshot.Interlocks[i].EventID == snapshot.Interlocks[j].EventID {
			return snapshot.Interlocks[i].Name < snapshot.Interlocks[j].Name
		}
		return snapshot.Interlocks[i].EventID < snapshot.Interlocks[j].EventID
	})
	return snapshot
}

func (runtime *Runtime) Operations() []model.OperationView {
	return runtime.Snapshot().Operations
}

func (runtime *Runtime) Equipment() []model.EquipmentView {
	return runtime.Snapshot().Equipment
}

func (runtime *Runtime) Interlocks() []model.InterlockView {
	return runtime.Snapshot().Interlocks
}

func (runtime *Runtime) Incidents() []model.IncidentView {
	return runtime.Snapshot().Incidents
}

func (runtime *Runtime) sourceIdentityInterlock(eventID model.EventID) model.InterlockView {
	interlock := model.InterlockView{EventID: eventID, Name: "source-identity", Passed: false, Detail: "source identity has not been verified"}
	expected, hasPlan := runtime.Verification.Expected(eventID)
	latest, hasResult := runtime.Verification.Latest(eventID)
	switch {
	case !hasPlan:
		interlock.Detail = "no expected source plan is recorded for this event"
	case !hasResult:
		interlock.Detail = "source plan changed; verification required"
	case latest.Fingerprint != expected.Fingerprint():
		interlock.Detail = "source plan changed after the last verification; re-verify before going on air"
	case latest.Passed:
		interlock.Passed = true
		interlock.Detail = latest.Reason
	default:
		interlock.Detail = latest.Reason
	}
	return interlock
}
