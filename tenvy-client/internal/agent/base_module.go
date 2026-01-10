package agent

import (
	"context"
	"sync"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type BaseModule struct {
	mu         sync.RWMutex
	id         string
	runtime    Config
	extensions *moduleExtensionState
	telemetry  *moduleTelemetryRegistry
	extOnce    sync.Once
	telOnce    sync.Once
}

func NewBaseModule(id string, baseCapabilities []string) *BaseModule {
	m := &BaseModule{id: id}
	m.extensions = newModuleExtensionState(baseCapabilities)
	return m
}

func (m *BaseModule) ID() string {
	return m.id
}

func (m *BaseModule) Init(_ context.Context, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtime = cfg
	return nil
}

func (m *BaseModule) UpdateConfig(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtime = cfg
	return nil
}

func (m *BaseModule) Shutdown(_ context.Context) error {
	return nil
}

func (m *BaseModule) ExtensionState() *moduleExtensionState {
	return m.extensions
}

func (m *BaseModule) RegisterExtension(extension ModuleExtension) error {
	return m.extensions.register(extension)
}

func (m *BaseModule) UnregisterExtension(source string) error {
	return m.extensions.unregister(source)
}

func (m *BaseModule) TelemetryRegistry() *moduleTelemetryRegistry {
	m.telOnce.Do(func() {
		if m.telemetry == nil {
			m.telemetry = newModuleTelemetryRegistry()
		}
	})
	return m.telemetry
}

func (m *BaseModule) RegisterTelemetry(source string, descriptors []ModuleTelemetryDescriptor) error {
	return m.TelemetryRegistry().register(source, descriptors)
}

func (m *BaseModule) UnregisterTelemetry(source string) error {
	return m.TelemetryRegistry().unregister(source)
}

func (m *BaseModule) Runtime() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtime
}

func (m *BaseModule) HandleCapabilityCheck(cmd protocol.Command, required ...string) error {
	if !m.extensions.hasAnyCapability(required...) {
		return WrapCommandResult(capabilityUnavailableResult(cmd, m.id, required...))
	}
	return nil
}
