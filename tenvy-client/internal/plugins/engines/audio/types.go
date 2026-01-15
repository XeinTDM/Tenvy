package audioengine

import (
	"context"
	"net/http"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type Engine interface {
	Configure(cfg Config) error
	HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult
	Shutdown()
}

type Logger interface {
	Printf(format string, args ...interface{})
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	AgentID        string
	BaseURL        string
	AuthKey        string
	Client         HTTPDoer
	Logger         Logger
	UserAgent      string
	RequestTimeout time.Duration
}

type AudioDirection string

const (
	AudioDirectionInput  AudioDirection = "input"
	AudioDirectionOutput AudioDirection = "output"
)

type AudioStreamFormat struct {
	Encoding   string `json:"encoding" msgpack:"encoding"`
	SampleRate int    `json:"sampleRate" msgpack:"sampleRate"`
	Channels   int    `json:"channels" msgpack:"channels"`
}

type AudioStreamTransport struct {
	Transport string            `json:"transport" msgpack:"transport"`
	URL       string            `json:"url" msgpack:"url"`
	Protocol  string            `json:"protocol,omitempty" msgpack:"protocol,omitempty"`
	Headers   map[string]string `json:"headers,omitempty" msgpack:"headers,omitempty"`
}

type AudioControlCommandPayload struct {
	Action          string                `json:"action"`
	RequestID       string                `json:"requestId,omitempty"`
	SessionID       string                `json:"sessionId,omitempty"`
	DeviceID        string                `json:"deviceId,omitempty"`
	DeviceLabel     string                `json:"deviceLabel,omitempty"`
	Direction       AudioDirection        `json:"direction,omitempty"`
	Channels        int                   `json:"channels,omitempty"`
	SampleRate      int                   `json:"sampleRate,omitempty"`
	Encoding        string                `json:"encoding,omitempty"`
	StreamTransport *AudioStreamTransport `json:"streamTransport,omitempty"`
	TrackID         string                `json:"trackId,omitempty"`
	TrackURL        string                `json:"trackUrl,omitempty"`
	Volume          float64               `json:"volume,omitempty"`
	Loop            bool                  `json:"loop,omitempty"`
	OutputDeviceID  string                `json:"outputDeviceId,omitempty"`
}

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

type AudioStreamChunk struct {
	SessionID string            `json:"sessionId" msgpack:"sessionId"`
	Sequence  uint64            `json:"sequence" msgpack:"sequence"`
	Timestamp string            `json:"timestamp" msgpack:"timestamp"`
	Format    AudioStreamFormat `json:"format" msgpack:"format"`
	Data      []byte            `json:"data" msgpack:"data"`
}

type AudioDiagnosticResult struct {
	Inventory     *AudioDeviceInventory `json:"inventory,omitempty"`
	Duration      time.Duration         `json:"duration"`
	BytesCaptured uint64                `json:"bytesCaptured"`
}