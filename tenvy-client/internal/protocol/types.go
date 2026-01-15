package protocol

import (
	"encoding/json"
	"errors"
	"strings"

	options "github.com/rootbay/tenvy-client/internal/operations/options"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

const (
	CommandStreamSubprotocol    = "tenvy.agent.v1"
	CommandStreamMaxMessageSize = 1 << 20 // 1 MiB
	AudioStreamSubprotocol      = "tenvy.audio.v1"
	AudioStreamTokenHeader      = "X-Audio-Stream-Token"
)

var ErrUnauthorized = errors.New("unauthorized")

type PluginSignaturePolicy struct {
	SHA256AllowList   []string          `json:"sha256AllowList,omitempty" msgpack:"sha256AllowList,omitempty"`
	Ed25519PublicKeys map[string]string `json:"ed25519PublicKeys,omitempty" msgpack:"ed25519PublicKeys,omitempty"`
	MaxSignatureAgeMs int64             `json:"maxSignatureAgeMs,omitempty" msgpack:"maxSignatureAgeMs,omitempty"`
}

type PluginConfig struct {
	SignaturePolicy *PluginSignaturePolicy `json:"signaturePolicy,omitempty" msgpack:"signaturePolicy,omitempty"`
}

type AgentConfig struct {
	PollIntervalMs int           `json:"pollIntervalMs" msgpack:"pollIntervalMs"`
	MaxBackoffMs   int           `json:"maxBackoffMs" msgpack:"maxBackoffMs"`
	JitterRatio    float64       `json:"jitterRatio" msgpack:"jitterRatio"`
	Plugins        *PluginConfig `json:"plugins,omitempty" msgpack:"plugins,omitempty"`
}

type AgentMetrics struct {
	MemoryBytes   uint64 `json:"memoryBytes,omitempty" msgpack:"memoryBytes,omitempty"`
	Goroutines    int    `json:"goroutines,omitempty" msgpack:"goroutines,omitempty"`
	UptimeSeconds uint64 `json:"uptimeSeconds,omitempty" msgpack:"uptimeSeconds,omitempty"`
}

type Command struct {
	ID        string          `json:"id" msgpack:"id"`
	Name      string          `json:"name" msgpack:"name"`
	Payload   json.RawMessage `json:"payload" msgpack:"payload"`
	CreatedAt string          `json:"createdAt" msgpack:"createdAt"`
	Signature string          `json:"signature,omitempty" msgpack:"signature,omitempty"`
}

type CommandEnvelope struct {
	Type        string                   `json:"type"`
	Command     *Command                 `json:"command,omitempty"`
	Input       *RemoteDesktopInputBurst `json:"input,omitempty"`
	AppVncInput *AppVncInputBurst        `json:"appVncInput,omitempty"`
}

type EncryptedEnvelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func (e *CommandEnvelope) UnmarshalJSON(data []byte) error {
	if e == nil {
		return errors.New("command envelope not initialized")
	}

	type alias CommandEnvelope
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	e.Type = aux.Type
	e.Command = aux.Command
	e.Input = aux.Input
	e.AppVncInput = aux.AppVncInput

	if e.Input == nil && e.AppVncInput == nil {
		var raw struct {
			Input json.RawMessage `json:"input,omitempty"`
		}
		if err := json.Unmarshal(data, &raw); err == nil && len(raw.Input) > 0 {
			switch strings.ToLower(strings.TrimSpace(e.Type)) {
			case "remote-desktop-input":
				_ = json.Unmarshal(raw.Input, &e.Input)
			case "app-vnc-input":
				_ = json.Unmarshal(raw.Input, &e.AppVncInput)
			}
		}
	}

	return nil
}

func UnmarshalPayload(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

type CommandResult struct {
	CommandID   string `json:"commandId" msgpack:"commandId"`
	Success     bool   `json:"success" msgpack:"success"`
	Output      string `json:"output,omitempty" msgpack:"output,omitempty"`
	Error       string `json:"error,omitempty" msgpack:"error,omitempty"`
	CompletedAt string `json:"completedAt" msgpack:"completedAt"`
}

type CommandOutputEvent struct {
	Type      string         `json:"type"`
	CommandID string         `json:"commandId"`
	Sequence  int64          `json:"sequence,omitempty"`
	Data      string         `json:"data,omitempty"`
	Timestamp string         `json:"timestamp"`
	Result    *CommandResult `json:"result,omitempty"`
}

type RemoteDesktopInputType string

const (
	RemoteDesktopInputMouseMove   RemoteDesktopInputType = "mouse-move"
	RemoteDesktopInputMouseButton RemoteDesktopInputType = "mouse-button"
	RemoteDesktopInputMouseScroll RemoteDesktopInputType = "mouse-scroll"
	RemoteDesktopInputKey         RemoteDesktopInputType = "key"
)

type RemoteDesktopInputEvent struct {
	Type       RemoteDesktopInputType `json:"type"`
	CapturedAt int64                  `json:"capturedAt"`
	X          float64                `json:"x,omitempty"`
	Y          float64                `json:"y,omitempty"`
	Normalized bool                   `json:"normalized,omitempty"`
	Monitor    *int                   `json:"monitor,omitempty"`
	Button     string                 `json:"button,omitempty"`
	Pressed    bool                   `json:"pressed,omitempty"`
	DeltaX     float64                `json:"deltaX,omitempty"`
	DeltaY     float64                `json:"deltaY,omitempty"`
	DeltaMode  int                    `json:"deltaMode,omitempty"`
	Key        string                 `json:"key,omitempty"`
	Code       string                 `json:"code,omitempty"`
	KeyCode    int                    `json:"keyCode,omitempty"`
	Repeat     bool                   `json:"repeat,omitempty"`
	AltKey     bool                   `json:"altKey,omitempty"`
	CtrlKey    bool                   `json:"ctrlKey,omitempty"`
	ShiftKey   bool                   `json:"shiftKey,omitempty"`
	MetaKey    bool                   `json:"metaKey,omitempty"`
}

type RemoteDesktopInputBurst struct {
	SessionID string                    `json:"sessionId"`
	Sequence  int64                     `json:"sequence,omitempty"`
	Events    []RemoteDesktopInputEvent `json:"events"`
}

type WebcamQuality string

const (
	WebcamQualityMax    WebcamQuality = "max"
	WebcamQualityHigh   WebcamQuality = "high"
	WebcamQualityMedium WebcamQuality = "medium"
	WebcamQualityLow    WebcamQuality = "low"
)

type WebcamResolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type WebcamZoomRange struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Step float64 `json:"step"`
}

type WebcamDeviceCapabilities struct {
	Resolutions []WebcamResolution `json:"resolutions,omitempty"`
	FrameRates  []float64          `json:"frameRates,omitempty"`
	Zoom        *WebcamZoomRange   `json:"zoom,omitempty"`
	FacingMode  string             `json:"facingMode,omitempty"`
}

type WebcamDevice struct {
	ID           string                    `json:"id"`
	Label        string                    `json:"label"`
	Capabilities *WebcamDeviceCapabilities `json:"capabilities,omitempty"`
}

type WebcamDeviceInventory struct {
	Devices    []WebcamDevice `json:"devices"`
	CapturedAt string         `json:"capturedAt"`
	RequestID  string         `json:"requestId,omitempty"`
	Warning    string         `json:"warning,omitempty"`
}

type WebcamStreamSettings struct {
	Quality     WebcamQuality `json:"quality,omitempty"`
	Width       int           `json:"width,omitempty"`
	Height      int           `json:"height,omitempty"`
	FrameRate   float64       `json:"frameRate,omitempty"`
	Zoom        float64       `json:"zoom,omitempty"`
	MimeType    string        `json:"mimeType,omitempty"`
	PixelFormat string        `json:"pixelFormat,omitempty"`
}

type WebcamNegotiationOffer struct {
	Transport   string   `json:"transport"`
	Offer       string   `json:"offer,omitempty"`
	IceServers  []string `json:"iceServers,omitempty"`
	DataChannel string   `json:"dataChannel,omitempty"`
}

type WebcamNegotiationAnswer struct {
	Answer      string   `json:"answer,omitempty"`
	IceServers  []string `json:"iceServers,omitempty"`
	DataChannel string   `json:"dataChannel,omitempty"`
}

type WebcamNegotiationState struct {
	Offer  *WebcamNegotiationOffer  `json:"offer,omitempty"`
	Answer *WebcamNegotiationAnswer `json:"answer,omitempty"`
}

type WebcamCommandPayload struct {
	Action      string                  `json:"action"`
	RequestID   string                  `json:"requestId,omitempty"`
	SessionID   string                  `json:"sessionId,omitempty"`
	DeviceID    string                  `json:"deviceId,omitempty"`
	Settings    *WebcamStreamSettings   `json:"settings,omitempty"`
	Negotiation *WebcamNegotiationState `json:"negotiation,omitempty"`
}

type AppVncInputBurst struct {
	SessionID string             `json:"sessionId"`
	Events    []AppVncInputEvent `json:"events"`
	Sequence  int64              `json:"sequence,omitempty"`
}

type AppVncQuality string

const (
	AppVncQualityLossless  AppVncQuality = "lossless"
	AppVncQualityBalanced  AppVncQuality = "balanced"
	AppVncQualityBandwidth AppVncQuality = "bandwidth"
)

type AppVncPlatform string

const (
	AppVncPlatformWindows AppVncPlatform = "windows"
	AppVncPlatformLinux   AppVncPlatform = "linux"
	AppVncPlatformMacOS   AppVncPlatform = "macos"
)

type AppVncSessionSettings struct {
	Monitor           string        `json:"monitor"`
	Quality           AppVncQuality `json:"quality"`
	CaptureCursor     bool          `json:"captureCursor"`
	ClipboardSync     bool          `json:"clipboardSync"`
	BlockLocalInput   bool          `json:"blockLocalInput"`
	HeartbeatInterval int           `json:"heartbeatInterval"`
	AppID             string        `json:"appId,omitempty"`
	WindowTitle       string        `json:"windowTitle,omitempty"`
}

type AppVncSessionSettingsPatch struct {
	Monitor           *string        `json:"monitor,omitempty"`
	Quality           *AppVncQuality `json:"quality,omitempty"`
	CaptureCursor     *bool          `json:"captureCursor,omitempty"`
	ClipboardSync     *bool          `json:"clipboardSync,omitempty"`
	BlockLocalInput   *bool          `json:"blockLocalInput,omitempty"`
	HeartbeatInterval *int           `json:"heartbeatInterval,omitempty"`
	AppID             *string        `json:"appId,omitempty"`
	WindowTitle       *string        `json:"windowTitle,omitempty"`
}

type AppVncSessionMetadata struct {
	AppID          string `json:"appId,omitempty"`
	WindowTitle    string `json:"windowTitle,omitempty"`
	ProcessID      int    `json:"processId,omitempty"`
	VirtualDisplay bool   `json:"virtualDisplay,omitempty"`
}

type AppVncCursorState struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Visible bool    `json:"visible"`
}

type AppVncFramePacket struct {
	SessionID string                 `json:"sessionId"`
	Sequence  int64                  `json:"sequence"`
	Timestamp string                 `json:"timestamp"`
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	Encoding  string                 `json:"encoding"`
	Image     string                 `json:"image"`
	Cursor    *AppVncCursorState     `json:"cursor,omitempty"`
	Metadata  *AppVncSessionMetadata `json:"metadata,omitempty"`
}

type AppVncVirtualizationHints struct {
	ProfileSeeds map[AppVncPlatform]string            `json:"profileSeeds,omitempty"`
	DataRoots    map[AppVncPlatform]string            `json:"dataRoots,omitempty"`
	Environment  map[AppVncPlatform]map[string]string `json:"environment,omitempty"`
}

type AppVncVirtualizationPlan struct {
	Platform    AppVncPlatform    `json:"platform,omitempty"`
	ProfileSeed string            `json:"profileSeed,omitempty"`
	DataRoot    string            `json:"dataRoot,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

type AppVncApplicationDescriptor struct {
	ID              string                     `json:"id"`
	Name            string                     `json:"name"`
	Summary         string                     `json:"summary"`
	Category        string                     `json:"category"`
	Platforms       []AppVncPlatform           `json:"platforms"`
	WindowTitleHint string                     `json:"windowTitleHint,omitempty"`
	Executable      map[AppVncPlatform]string  `json:"executable,omitempty"`
	Virtualization  *AppVncVirtualizationHints `json:"virtualization,omitempty"`
}

type AppVncPointerButton string

const (
	AppVncPointerButtonLeft   AppVncPointerButton = "left"
	AppVncPointerButtonMiddle AppVncPointerButton = "middle"
	AppVncPointerButtonRight  AppVncPointerButton = "right"
)

type AppVncInputEventType string

const (
	AppVncInputPointerMove   AppVncInputEventType = "pointer-move"
	AppVncInputPointerButton AppVncInputEventType = "pointer-button"
	AppVncInputPointerScroll AppVncInputEventType = "pointer-scroll"
	AppVncInputKey           AppVncInputEventType = "key"
)

type AppVncInputEvent struct {
	Type       AppVncInputEventType `json:"type"`
	CapturedAt int64                `json:"capturedAt"`
	X          float64              `json:"x,omitempty"`
	Y          float64              `json:"y,omitempty"`
	Normalized bool                 `json:"normalized,omitempty"`
	Button     AppVncPointerButton  `json:"button,omitempty"`
	Pressed    bool                 `json:"pressed,omitempty"`
	DeltaX     float64              `json:"deltaX,omitempty"`
	DeltaY     float64              `json:"deltaY,omitempty"`
	DeltaMode  int                  `json:"deltaMode,omitempty"`
	Key        string               `json:"key,omitempty"`
	Code       string               `json:"code,omitempty"`
	KeyCode    int                  `json:"keyCode,omitempty"`
	Repeat     bool                 `json:"repeat,omitempty"`
	AltKey     bool                 `json:"altKey,omitempty"`
	CtrlKey    bool                 `json:"ctrlKey,omitempty"`
	ShiftKey   bool                 `json:"shiftKey,omitempty"`
	MetaKey    bool                 `json:"metaKey,omitempty"`
}

type AppVncCommandPayload struct {
	Action         string                       `json:"action"`
	SessionID      string                       `json:"sessionId,omitempty"`
	Settings       *AppVncSessionSettingsPatch  `json:"settings,omitempty"`
	Events         []AppVncInputEvent           `json:"events,omitempty"`
	Application    *AppVncApplicationDescriptor `json:"application,omitempty"`
	Virtualization *AppVncVirtualizationPlan    `json:"virtualization,omitempty"`
}

type AgentMetadata struct {
	Hostname        string   `json:"hostname"`
	Username        string   `json:"username"`
	OS              string   `json:"os"`
	Architecture    string   `json:"architecture"`
	IPAddress       string   `json:"ipAddress,omitempty"`
	PublicIPAddress string   `json:"publicIpAddress,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Version         string   `json:"version,omitempty"`
	HardwareID      string   `json:"hardwareId,omitempty"`
	Analysis        string   `json:"analysis,omitempty"`
}

type AgentRegistrationRequest struct {
	Token     string        `json:"token,omitempty"`
	Metadata  AgentMetadata `json:"metadata"`
	PublicKey *string       `json:"publicKey,omitempty"`
}

type AgentRegistrationResponse struct {
	AgentID         string      `json:"agentId"`
	AgentKey        string      `json:"agentKey"`
	Config          AgentConfig `json:"config"`
	Commands        []Command   `json:"commands"`
	ServerTime      string      `json:"serverTime"`
	ServerPublicKey *string     `json:"serverPublicKey,omitempty"`
}

type AgentSyncRequest struct {
	Status    string                `json:"status" msgpack:"status"`
	Timestamp string                `json:"timestamp" msgpack:"timestamp"`
	Metrics   *AgentMetrics         `json:"metrics,omitempty" msgpack:"metrics,omitempty"`
	Results   []CommandResult       `json:"results,omitempty" msgpack:"results,omitempty"`
	Plugins   *manifest.SyncPayload `json:"plugins,omitempty" msgpack:"plugins,omitempty"`
	Options   *options.State        `json:"options,omitempty" msgpack:"options,omitempty"`
}

type AgentSyncResponse struct {
	AgentID         string                  `json:"agentId" msgpack:"agentId"`
	Commands        []Command               `json:"commands" msgpack:"commands"`
	Config          AgentConfig             `json:"config" msgpack:"config"`
	ServerTime      string                  `json:"serverTime" msgpack:"serverTime"`
	PluginManifests *manifest.ManifestDelta `json:"pluginManifests,omitempty" msgpack:"pluginManifests,omitempty"`
	Options         *options.State          `json:"options,omitempty" msgpack:"options,omitempty"`
}

type PingCommandPayload struct {
	Message string `json:"message,omitempty"`
}

type ShellCommandPayload struct {
	Command          string            `json:"command"`
	TimeoutSeconds   int               `json:"timeoutSeconds,omitempty"`
	WorkingDirectory string            `json:"workingDirectory,omitempty"`
	Elevated         bool              `json:"elevated,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
}

type OpenURLCommandPayload struct {
	URL  string `json:"url"`
	Note string `json:"note,omitempty"`
}

type AgentControlCommandPayload struct {
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

type ToolActivationCommandPayload struct {
	ToolID      string         `json:"toolId"`
	Action      string         `json:"action"`
	InitiatedBy string         `json:"initiatedBy,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type RecoveryTargetSelection struct {
	Type      string   `json:"type"`
	Label     string   `json:"label,omitempty"`
	Path      string   `json:"path,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Recursive bool     `json:"recursive,omitempty"`
}

type RecoveryCommandPayload struct {
	RequestID   string                    `json:"requestId"`
	Selections  []RecoveryTargetSelection `json:"selections"`
	ArchiveName string                    `json:"archiveName,omitempty"`
	Notes       string                    `json:"notes,omitempty"`
}

type RecoveryManifestEntry struct {
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	ModifiedAt      string `json:"modifiedAt"`
	Mode            string `json:"mode"`
	Type            string `json:"type"`
	Target          string `json:"target"`
	SourcePath      string `json:"sourcePath,omitempty"`
	Preview         string `json:"preview,omitempty"`
	PreviewEncoding string `json:"previewEncoding,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
}

type ClientChatAliasConfiguration struct {
	Operator string `json:"operator,omitempty"`
	Client   string `json:"client,omitempty"`
}

type ClientChatFeatureFlags struct {
	Unstoppable        *bool `json:"unstoppable,omitempty"`
	AllowNotifications *bool `json:"allowNotifications,omitempty"`
	AllowFileTransfers *bool `json:"allowFileTransfers,omitempty"`
}

type ClientChatCommandMessage struct {
	ID        string `json:"id,omitempty"`
	Body      string `json:"body"`
	Timestamp string `json:"timestamp,omitempty"`
	Alias     string `json:"alias,omitempty"`
}

type ClientChatCommandPayload struct {
	Action    string                        `json:"action"`
	SessionID string                        `json:"sessionId,omitempty"`
	Message   *ClientChatCommandMessage     `json:"message,omitempty"`
	Aliases   *ClientChatAliasConfiguration `json:"aliases,omitempty"`
	Features  *ClientChatFeatureFlags       `json:"features,omitempty"`
}

type ClientChatMessage struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	Timestamp string `json:"timestamp"`
	Alias     string `json:"alias,omitempty"`
}

type ClientChatMessageEnvelope struct {
	SessionID string            `json:"sessionId"`
	Message   ClientChatMessage `json:"message"`
}
