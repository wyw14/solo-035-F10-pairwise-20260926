package verify

import (
	"sync"

	"github.com/wyw14/solo-035/internal/model"
)

type State struct {
	mu     sync.RWMutex
	latest map[model.EventID]model.SourceCheck
}

func NewState() *State {
	return &State{latest: make(map[model.EventID]model.SourceCheck)}
}

func (state *State) Record(check model.SourceCheck) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.latest[check.EventID] = check
}

func (state *State) Latest(eventID model.EventID) (model.SourceCheck, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	check, ok := state.latest[eventID]
	return check, ok
}
