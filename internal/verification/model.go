package verification

import (
	"time"

	"github.com/wyw14/solo-035/internal/model"
)

// Expected is the source identity recorded while the program is prepared.
type Expected struct {
	EventID    model.EventID    `json:"event_id"`
	Source     model.SourceID   `json:"source"`
	Generation uint64           `json:"generation"`
	Revision   model.RevisionID `json:"revision"`
	Fence      uint64           `json:"fence"`
	PlannedAt  time.Time        `json:"planned_at"`
}

// Fingerprint identifies a source plan; any replan or generation update
// produces a new fingerprint and invalidates earlier confirmations.
type Fingerprint struct {
	Source     model.SourceID   `json:"source"`
	Generation uint64           `json:"generation"`
	Revision   model.RevisionID `json:"revision"`
	Fence      uint64           `json:"fence"`
	PlannedAt  time.Time        `json:"planned_at"`
}

func (expected Expected) Fingerprint() Fingerprint {
	return Fingerprint{
		Source:     expected.Source,
		Generation: expected.Generation,
		Revision:   expected.Revision,
		Fence:      expected.Fence,
		PlannedAt:  expected.PlannedAt,
	}
}

// FrameEvidence is the source metadata of the most recent frame the event
// actually received.
type FrameEvidence struct {
	Present    bool           `json:"present"`
	Source     model.SourceID `json:"source"`
	Generation uint64         `json:"generation"`
	Sequence   int64          `json:"sequence"`
	Timecode   int64          `json:"timecode"`
	CapturedAt time.Time      `json:"captured_at"`
}

// LockEvidence is the receiver lock state for the planned source.
type LockEvidence struct {
	Present        bool            `json:"present"`
	Source         model.SourceID  `json:"source"`
	Generation     uint64          `json:"generation"`
	CurrentCommand model.CommandID `json:"current_command"`
	VideoLocked    bool            `json:"video_locked"`
	AudioLocked    bool            `json:"audio_locked"`
}

// OutputEvidence is the prepared playout output the commit would publish.
type OutputEvidence struct {
	Present  bool             `json:"present"`
	EventID  model.EventID    `json:"event_id"`
	Source   model.SourceID   `json:"source"`
	Revision model.RevisionID `json:"revision"`
	Fence    uint64           `json:"fence"`
	OnAir    bool             `json:"on_air"`
}

// Observation bundles every piece of runtime metadata compared against the
// planned source identity.
type Observation struct {
	EventID               model.EventID    `json:"event_id"`
	EventRevision         model.RevisionID `json:"event_revision"`
	EventSourceGeneration uint64           `json:"event_source_generation"`
	Frame                 FrameEvidence    `json:"frame"`
	Lock                  LockEvidence     `json:"lock"`
	Output                OutputEvidence   `json:"output"`
	ReceiverLastFrame     *FrameEvidence   `json:"receiver_last_frame,omitempty"`
}

// Result is the durable evidence of a single verification attempt.
type Result struct {
	Expected    Expected    `json:"expected"`
	Observed    Observation `json:"observed"`
	Passed      bool        `json:"passed"`
	Reason      string      `json:"reason"`
	Fingerprint Fingerprint `json:"fingerprint"`
	CheckedAt   time.Time   `json:"checked_at"`
	JournalID   string      `json:"journal_id,omitempty"`
}

const confirmedReason = "source identity confirmed"

// Evaluate compares the planned source identity against the observed receiver
// metadata, lock state and prepared output. The first failed check becomes the
// result reason; sequence and timecode are recorded as evidence only.
func Evaluate(expected Expected, obs Observation, now time.Time) Result {
	result := Result{
		Expected:    expected,
		Observed:    obs,
		Fingerprint: expected.Fingerprint(),
		CheckedAt:   now.UTC(),
	}
	switch {
	case !obs.Output.Present:
		result.Reason = "no playout output is prepared"
	case obs.Output.Source != expected.Source:
		result.Reason = "prepared output source does not match planned camera"
	case obs.Output.Revision != expected.Revision:
		result.Reason = "prepared output revision does not match program plan"
	case obs.Output.Fence != expected.Fence:
		result.Reason = "prepared output fence does not match encoder lease"
	case !obs.Frame.Present:
		result.Reason = "receiver has no recent frame for the event"
	case obs.Frame.Source != expected.Source:
		result.Reason = "most recent frame arrived from another camera"
	case obs.Frame.Generation != expected.Generation:
		result.Reason = "most recent frame belongs to another source generation"
	case !obs.Lock.Present:
		result.Reason = "planned source has no receiver lock status"
	case obs.Lock.Generation != expected.Generation:
		result.Reason = "receiver lock belongs to another source generation"
	case !obs.Lock.VideoLocked || !obs.Lock.AudioLocked:
		result.Reason = "planned source does not hold complete media lock"
	case obs.EventRevision != expected.Revision:
		result.Reason = "program revision advanced after source was verified"
	case obs.EventSourceGeneration != expected.Generation:
		result.Reason = "source generation advanced after source was verified"
	default:
		result.Passed = true
		result.Reason = confirmedReason
	}
	return result
}
