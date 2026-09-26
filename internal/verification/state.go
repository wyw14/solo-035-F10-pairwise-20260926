package verification

import (
	"fmt"
	"sync"

	"github.com/wyw14/solo-035/internal/model"
)

const historyLimit = 32

type entry struct {
	expected Expected
	latest   *Result
	history  []Result
}

// State retains the planned source identity, the latest verification result
// and a bounded history for each event.
type State struct {
	mu      sync.RWMutex
	entries map[model.EventID]*entry
}

func NewState() *State {
	return &State{entries: make(map[model.EventID]*entry)}
}

// Plan records the expected source identity and clears the previous result, so
// a plan change always requires a fresh verification.
func (state *State) Plan(expected Expected) error {
	if err := validateExpected(expected); err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	item := state.entries[expected.EventID]
	if item == nil {
		item = &entry{}
		state.entries[expected.EventID] = item
	}
	item.expected = expected
	item.latest = nil
	return nil
}

// Invalidate drops the plan and latest result after a source switch or a
// program/source generation advance. The history is retained as evidence.
func (state *State) Invalidate(eventID model.EventID) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if item := state.entries[eventID]; item != nil {
		item.expected = Expected{}
		item.latest = nil
	}
}

// InvalidateSource drops every plan that currently points at the source, used
// when a receiver is retired without an event-scoped signal.
func (state *State) InvalidateSource(sourceID model.SourceID) {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, item := range state.entries {
		if item.expected.Source == sourceID {
			item.expected = Expected{}
			item.latest = nil
		}
	}
}

// Save stores a verification result as the latest result and appends it to the
// history.
func (state *State) Save(result Result) {
	state.mu.Lock()
	defer state.mu.Unlock()
	item := state.entries[result.Expected.EventID]
	if item == nil {
		item = &entry{expected: result.Expected}
		state.entries[result.Expected.EventID] = item
	}
	saved := result
	item.latest = &saved
	item.history = append(item.history, saved)
	if len(item.history) > historyLimit {
		item.history = append([]Result(nil), item.history[len(item.history)-historyLimit:]...)
	}
}

// AttachJournal links the latest result to its durable journal record.
func (state *State) AttachJournal(eventID model.EventID, journalID string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if item := state.entries[eventID]; item != nil && item.latest != nil {
		item.latest.JournalID = journalID
	}
}

func (state *State) Expected(eventID model.EventID) (Expected, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	item := state.entries[eventID]
	if item == nil || item.expected.EventID == "" {
		return Expected{}, false
	}
	return item.expected, true
}

func (state *State) Latest(eventID model.EventID) (Result, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	item := state.entries[eventID]
	if item == nil || item.latest == nil {
		return Result{}, false
	}
	return *item.latest, true
}

func (state *State) History(eventID model.EventID) []Result {
	state.mu.RLock()
	defer state.mu.RUnlock()
	item := state.entries[eventID]
	if item == nil {
		return nil
	}
	return append([]Result(nil), item.history...)
}

func validateExpected(expected Expected) error {
	if !expected.EventID.Valid() || !expected.Source.Valid() || !expected.Revision.Valid() {
		return fmt.Errorf("expected source identity is incomplete")
	}
	if expected.Generation == 0 || expected.Fence == 0 {
		return fmt.Errorf("expected source generation and fence are required")
	}
	return nil
}
