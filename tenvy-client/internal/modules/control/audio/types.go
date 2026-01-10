package audio

import "time"

type AudioDirection string

const (
	AudioDirectionInput  AudioDirection = "input"
	AudioDirectionOutput AudioDirection = "output"
)

type AudioDeviceDescriptor struct {
	ID                    string         `json:"id"`
	DeviceID              string         `json:"deviceId"`
	Label                 string         `json:"label"`
	Kind                  AudioDirection `json:"kind"`
	GroupID               string         `json:"groupId"`
	SystemDefault         bool           `json:"systemDefault"`
	CommunicationsDefault bool           `json:"communicationsDefault"`
	LastSeen              string         `json:"lastSeen"`
}

type AudioDeviceInventory struct {
	Inputs     []AudioDeviceDescriptor `json:"inputs"`
	Outputs    []AudioDeviceDescriptor `json:"outputs"`
	CapturedAt string                  `json:"capturedAt"`
	RequestID  string                  `json:"requestId,omitempty"`
}

type AudioStreamFormat struct {
	Encoding   string `json:"encoding" msgpack:"encoding"`
	SampleRate int    `json:"sampleRate" msgpack:"sampleRate"`
	Channels   int    `json:"channels" msgpack:"channels"`
}

type AudioStreamTransport struct {
	Transport string            `json:"transport"`
	URL       string            `json:"url"`
	Protocol  string            `json:"protocol,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type AudioStreamChunk struct {
	SessionID string            `json:"sessionId" msgpack:"sessionId"`
	Sequence  uint64            `json:"sequence" msgpack:"sequence"`
	Timestamp string            `json:"timestamp" msgpack:"timestamp"`
	Format    AudioStreamFormat `json:"format" msgpack:"format"`
	Data      []byte            `json:"data" msgpack:"data"`
}

type AudioControlCommandPayload struct {
	Action            string                `json:"action"`
	RequestID         string                `json:"requestId,omitempty"`
	SessionID         string                `json:"sessionId,omitempty"`
	DeviceID          string                `json:"deviceId,omitempty"`
	DeviceLabel       string                `json:"deviceLabel,omitempty"`
	Direction         AudioDirection        `json:"direction,omitempty"`
	SampleRate        int                   `json:"sampleRate,omitempty"`
	Channels          int                   `json:"channels,omitempty"`
	Encoding          string                `json:"encoding,omitempty"`
	StreamTransport   *AudioStreamTransport `json:"streamTransport,omitempty"`
	TrackID           string                `json:"trackId,omitempty"`
	TrackURL          string                `json:"trackUrl,omitempty"`
	OutputDeviceID    string                `json:"outputDeviceId,omitempty"`
	OutputDeviceLabel string                `json:"outputDeviceLabel,omitempty"`
	Volume            float64               `json:"volume,omitempty"`
	Loop              bool                  `json:"loop,omitempty"`
	ChaosMode         bool                  `json:"chaosMode,omitempty"`
	Rickroll          bool                  `json:"rickroll,omitempty"`
}

type AudioDiagnosticResult struct {
	Inventory     *AudioDeviceInventory
	BytesCaptured uint64
	Duration      time.Duration
}
