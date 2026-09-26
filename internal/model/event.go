package model

import (
	"errors"
	"fmt"
	"time"
)

type EventPhase string

const (
	PhasePreparing  EventPhase = "preparing"
	PhaseRehearsal  EventPhase = "rehearsal"
	PhaseArmed      EventPhase = "armed"
	PhaseOnAir      EventPhase = "on-air"
	PhaseHolding    EventPhase = "holding"
	PhaseFailedOver EventPhase = "failed-over"
	PhaseClosed     EventPhase = "closed"
)

var phaseTransitions = map[EventPhase]map[EventPhase]bool{
	PhasePreparing:  {PhaseRehearsal: true, PhaseClosed: true},
	PhaseRehearsal:  {PhaseArmed: true, PhasePreparing: true, PhaseClosed: true},
	PhaseArmed:      {PhaseOnAir: true, PhaseHolding: true, PhaseClosed: true},
	PhaseOnAir:      {PhaseHolding: true, PhaseFailedOver: true, PhaseClosed: true},
	PhaseHolding:    {PhaseOnAir: true, PhaseFailedOver: true, PhaseClosed: true},
	PhaseFailedOver: {PhaseOnAir: true, PhaseHolding: true, PhaseClosed: true},
	PhaseClosed:     {},
}

type LiveEvent struct {
	ID               EventID    `json:"id"`
	Name             string     `json:"name"`
	Phase            EventPhase `json:"phase"`
	Revision         RevisionID `json:"revision"`
	Source           SourceID   `json:"source"`
	CommandEpoch     uint64     `json:"command_epoch"`
	SourceGeneration uint64     `json:"source_generation"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func NewLiveEvent(name string, now time.Time) (LiveEvent, error) {
	if name == "" {
		return LiveEvent{}, errors.New("event name is required")
	}
	return LiveEvent{
		ID:               NewEventID(),
		Name:             name,
		Phase:            PhasePreparing,
		Revision:         NewRevisionID(),
		CommandEpoch:     1,
		SourceGeneration: 1,
		CreatedAt:        now.UTC(),
		UpdatedAt:        now.UTC(),
	}, nil
}

func (event LiveEvent) CanTransition(next EventPhase) bool {
	return phaseTransitions[event.Phase][next]
}

func (event LiveEvent) Transition(next EventPhase, now time.Time) (LiveEvent, error) {
	if !event.CanTransition(next) {
		return event, fmt.Errorf("event %s cannot transition from %s to %s", event.ID, event.Phase, next)
	}
	event.Phase = next
	event.CommandEpoch++
	event.UpdatedAt = now.UTC()
	return event, nil
}

func (event LiveEvent) NewProgram(now time.Time) LiveEvent {
	event.Revision = NewRevisionID()
	event.CommandEpoch++
	event.UpdatedAt = now.UTC()
	return event
}

func (event LiveEvent) NewSource(now time.Time) LiveEvent {
	event.SourceGeneration++
	event.CommandEpoch++
	event.UpdatedAt = now.UTC()
	return event
}
