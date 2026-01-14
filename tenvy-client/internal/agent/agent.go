package agent

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	geolocationmgr "github.com/rootbay/tenvy-client/internal/modules/misc/geolocation"
	notes "github.com/rootbay/tenvy-client/internal/modules/notes"
	options "github.com/rootbay/tenvy-client/internal/operations/options"
	"github.com/rootbay/tenvy-client/internal/plugins"
	"github.com/rootbay/tenvy-client/internal/protocol"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

type Agent struct {
	id                           string
	key                          string
	baseURL                      string
	client                       *http.Client
	config                       protocol.AgentConfig
	logger                       *log.Logger
	resultMu                     sync.Mutex
	pendingResults               []protocol.CommandResult
	resultStore                  *resultStore
	resultCacheSize              int
	startTime                    time.Time
	metadata                     protocol.AgentMetadata
	sharedSecret                 string
	preferences                  BuildPreferences
	notes                        *notes.Manager
	buildVersion                 string
	userAgentOverride            string
	userAgentFingerprint         string
	userAgentAutogenDisabled     bool
	timing                       TimingOverride
	modules                      *moduleManager
	commands                     *commandRouter
	connectionFlag               atomic.Uint32
	remoteDesktopInputOnce       sync.Once
	remoteDesktopInputSignalOnce sync.Once
	remoteDesktopInputQueue      chan remoteDesktopInputTask
	remoteDesktopInputStopCh     chan struct{}
	remoteDesktopInputStopped    atomic.Bool
	plugins                      *plugins.Manager
	requestHeaders               []CustomHeader
	requestCookies               []CustomCookie
	options                      *options.Manager
	scriptRunner                 *scriptRunner
	geolocationConfig            geolocationmgr.Config
	commandSecret                string
	commandPublicKey             string
	pluginManifestMu             sync.RWMutex
	pluginManifestVersion        string
	pluginManifestDigests        map[string]string
	pluginManifestDescriptors    map[string]manifest.ManifestDescriptor
}

func (a *Agent) AgentID() string {
	return a.id
}

func (a *Agent) AgentMetadata() protocol.AgentMetadata {
	return a.metadata
}

func (a *Agent) AgentStartTime() time.Time {
	return a.startTime
}

func (a *Agent) pluginSyncPayload() *manifest.SyncPayload {
	var installations []manifest.InstallationTelemetry
	if a.plugins != nil {
		if snapshot := a.plugins.Snapshot(); snapshot != nil && len(snapshot.Installations) > 0 {
			installations = snapshot.Installations
		}
	}

	state := a.currentManifestState()
	if len(installations) == 0 && state == nil {
		return nil
	}

	payload := &manifest.SyncPayload{}
	if len(installations) > 0 {
		payload.Installations = installations
	}
	if state != nil {
		payload.Manifests = state
	}
	return payload
}

func (a *Agent) currentManifestState() *manifest.ManifestState {
	a.pluginManifestMu.RLock()
	defer a.pluginManifestMu.RUnlock()

	if a.pluginManifestVersion == "" && len(a.pluginManifestDigests) == 0 {
		return nil
	}

	state := &manifest.ManifestState{Version: a.pluginManifestVersion}
	if len(a.pluginManifestDigests) > 0 {
		state.Digests = make(map[string]string, len(a.pluginManifestDigests))
		for id, digest := range a.pluginManifestDigests {
			state.Digests[id] = digest
		}
	}
	return state
}

func (a *Agent) setPluginManifestList(list *manifest.ManifestList) {
	a.pluginManifestMu.Lock()
	defer a.pluginManifestMu.Unlock()

	if list == nil {
		a.pluginManifestVersion = ""
		a.pluginManifestDigests = nil
		a.pluginManifestDescriptors = nil
		return
	}

	a.pluginManifestVersion = strings.TrimSpace(list.Version)

	if len(list.Manifests) == 0 {
		a.pluginManifestDigests = make(map[string]string)
		a.pluginManifestDescriptors = make(map[string]manifest.ManifestDescriptor)
		return
	}

	digests := make(map[string]string, len(list.Manifests))
	descriptors := make(map[string]manifest.ManifestDescriptor, len(list.Manifests))
	for _, entry := range list.Manifests {
		id := strings.TrimSpace(entry.PluginID)
		if id == "" {
			continue
		}
		digests[id] = manifestDescriptorFingerprint(entry)
		descriptors[id] = entry
	}
	a.pluginManifestDigests = digests
	a.pluginManifestDescriptors = descriptors
}

func (a *Agent) pluginManifestSnapshot() map[string]manifest.ManifestDescriptor {
	a.pluginManifestMu.RLock()
	defer a.pluginManifestMu.RUnlock()

	if len(a.pluginManifestDescriptors) == 0 {
		return nil
	}

	snapshot := make(map[string]manifest.ManifestDescriptor, len(a.pluginManifestDescriptors))
	for id, descriptor := range a.pluginManifestDescriptors {
		snapshot[id] = descriptor
	}
	return snapshot
}

func (a *Agent) moduleRuntime() Config {
	var activeModules []string
	if a.modules != nil {
		metadata := a.modules.Metadata()
		activeModules = make([]string, 0, len(metadata))
		for _, entry := range metadata {
			if id := strings.TrimSpace(entry.ID); id != "" {
				activeModules = append(activeModules, id)
			}
		}
	}

	var pluginHandles map[string]PluginActivationHandle
	if a.modules != nil {
		pluginHandles = a.modules.pluginHandleSnapshot()
	}

	return Config{
		AgentID:         a.id,
		BaseURL:         a.baseURL,
		AuthKey:         a.key,
		HTTPClient:      a.client,
		Logger:          a.logger,
		UserAgent:       a.userAgent(),
		Provider:        a,
		BuildVersion:    a.buildVersion,
		AgentConfig:     a.config,
		Plugins:         a.plugins,
		ActiveModules:   activeModules,
		PluginHandles:   pluginHandles,
		Extensions:      a.modules,
		PluginManifests: a.pluginManifestSnapshot(),
		Notes:           a.notes,
		Geolocation:     a.geolocationConfig,
	}
}
