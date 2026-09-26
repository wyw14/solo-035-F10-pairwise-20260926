package model

import "time"

type SourceCheck struct {
	EventID            EventID    `json:"event_id"`
	Revision           RevisionID `json:"revision"`
	ExpectedSource     SourceID   `json:"expected_source"`
	ExpectedGeneration uint64     `json:"expected_generation"`
	ObservedSource     SourceID   `json:"observed_source"`
	ObservedGeneration uint64     `json:"observed_generation"`
	ObservedKnown      bool       `json:"observed_known"`
	FrameKnown         bool       `json:"frame_known"`
	FrameSequence      int64      `json:"frame_sequence"`
	FrameTimecode      int64      `json:"frame_timecode"`
	FrameCapturedAt    time.Time  `json:"frame_captured_at"`
	SignalKnown        bool       `json:"signal_known"`
	SignalGeneration   uint64     `json:"signal_generation"`
	VideoLocked        bool       `json:"video_locked"`
	AudioLocked        bool       `json:"audio_locked"`
	StagedKnown        bool       `json:"staged_known"`
	StagedSource       SourceID   `json:"staged_source"`
	StagedGeneration   uint64     `json:"staged_generation"`
	StagedFence        uint64     `json:"staged_fence"`
	CommitFence        uint64     `json:"commit_fence"`
	Passed             bool       `json:"passed"`
	Reason             string     `json:"reason"`
	CheckedAt          time.Time  `json:"checked_at"`
}
