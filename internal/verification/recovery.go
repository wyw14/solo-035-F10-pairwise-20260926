package verification

import (
	"fmt"
)

// RecoveryRecord restores the planned source identity and the latest result;
// the full history lives durably in the journal.
type RecoveryRecord struct {
	Expected Expected `json:"expected"`
	Latest   *Result  `json:"latest,omitempty"`
}

func (state *State) ExportRecovery() []RecoveryRecord {
	state.mu.RLock()
	defer state.mu.RUnlock()
	records := make([]RecoveryRecord, 0, len(state.entries))
	for _, item := range state.entries {
		if item.expected.EventID == "" {
			continue
		}
		record := RecoveryRecord{Expected: item.expected}
		if item.latest != nil {
			latest := *item.latest
			record.Latest = &latest
		}
		records = append(records, record)
	}
	return records
}

func (state *State) RestoreRecovery(records []RecoveryRecord) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, record := range records {
		if !record.Expected.EventID.Valid() || !record.Expected.Source.Valid() || !record.Expected.Revision.Valid() {
			return fmt.Errorf("verification recovery record has invalid identity")
		}
		if record.Expected.Generation == 0 || record.Expected.Fence == 0 {
			return fmt.Errorf("verification recovery record for event %s is missing generation or fence", record.Expected.EventID)
		}
		item := &entry{expected: record.Expected}
		if record.Latest != nil {
			latest := *record.Latest
			item.latest = &latest
		}
		state.entries[record.Expected.EventID] = item
	}
	return nil
}
