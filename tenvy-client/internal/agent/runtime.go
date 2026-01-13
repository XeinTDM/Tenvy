package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	notes "github.com/rootbay/tenvy-client/internal/modules/notes"
	options "github.com/rootbay/tenvy-client/internal/operations/options"
	"github.com/rootbay/tenvy-client/internal/plugins"
	"github.com/rootbay/tenvy-client/internal/protocol"
	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
)

func Run(ctx context.Context, opts RuntimeOptions) error {
	runner := func(runCtx context.Context, runOpts RuntimeOptions) error {
		runOpts.ensureDefaults()
		if err := runOpts.Validate(); err != nil {
			return err
		}
		return runAgentOnce(runCtx, runOpts)
	}

	if opts.Watchdog.Enabled {
		return runWithWatchdog(ctx, opts, runner)
	}

	return runner(ctx, opts)
}

func runWithWatchdog(ctx context.Context, opts RuntimeOptions, runner func(context.Context, RuntimeOptions) error) error {
	interval := opts.Watchdog.Interval
	if interval <= 0 {
		interval = time.Minute
	}

	for {
		err := runner(ctx, opts)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if opts.Logger != nil {
			opts.Logger.Printf("agent terminated unexpectedly: %v; restarting in %s", err, interval)
		}
		if sleepErr := sleepContext(ctx, interval); sleepErr != nil {
			return sleepErr
		}
	}
}

func runAgentOnce(ctx context.Context, opts RuntimeOptions) error {
	normalizedServerURL, err := canonicalizeServerURL(opts.ServerURL)
	if err != nil {
		return err
	}
	opts.ServerURL = normalizedServerURL

	if err := enforceExecutionGates(ctx, opts.Logger, opts.Execution, opts.ServerURL); err != nil {
		return err
	}

	if err := enforcePrivilegeRequirement(opts.Preferences.ForceAdmin); err != nil {
		return err
	}

	mutexGuard, err := acquireInstanceMutex(opts.Preferences.MutexKey)
	if err != nil {
		return fmt.Errorf("failed to honor mutex preference: %w", err)
	}
	if mutexGuard != nil {
		defer mutexGuard.Release()
		description := "instance mutex guard"
		if name := mutexGuard.Name(); name != "" {
			description = fmt.Sprintf("instance mutex: %s", name)
		}
		if mutexGuard.Recovered() {
			opts.Logger.Printf("recovered stale %s", description)
		} else {
			opts.Logger.Printf("acquired %s", description)
		}
	}

	metadata := opts.Metadata
	if strings.TrimSpace(metadata.Hostname) == "" {
		metadata = CollectMetadataWithClient(opts.BuildVersion, opts.HTTPClient)
	}

	client := opts.HTTPClient

	fingerprint := normalizeFingerprintName(opts.userAgentFingerprint())
	disableAuto := opts.userAgentAutogenDisabled()
	fallbackVersion := strings.TrimSpace(metadata.Version)
	if fallbackVersion == "" {
		fallbackVersion = opts.BuildVersion
	}
	resolvedUserAgent := resolveUserAgentString(opts.UserAgentOverride, fingerprint, disableAuto, fallbackVersion)

	registration, err := registerAgentWithRetry(
		ctx,
		opts.Logger,
		client,
		opts.ServerURL,
		opts.SharedSecret,
		metadata,
		opts.maxBackoffOverride(),
		opts.CustomHeaders,
		opts.CustomCookies,
		resolvedUserAgent,
		disableAuto,
	)
	if err != nil {
		return fmt.Errorf("failed to register agent: %w", err)
	}

	store, err := newResultStore(resultStoreConfig{Path: opts.ResultStore.Path, Retention: opts.ResultStore.Retention})
	if err != nil {
		return fmt.Errorf("initialize result store: %w", err)
	}

	scriptDir := defaultScriptDirectory(opts.Preferences)

	agent := &Agent{
		id:                       registration.AgentID,
		key:                      registration.AgentKey,
		baseURL:                  opts.ServerURL,
		client:                   client,
		config:                   registration.Config,
		logger:                   opts.Logger,
		pendingResults:           make([]protocol.CommandResult, 0, opts.ResultStore.HotCache),
		resultStore:              store,
		resultCacheSize:          opts.ResultStore.HotCache,
		startTime:                time.Now(),
		metadata:                 metadata,
		sharedSecret:             opts.SharedSecret,
		preferences:              opts.Preferences,
		buildVersion:             opts.BuildVersion,
		userAgentOverride:        opts.UserAgentOverride,
		userAgentFingerprint:     fingerprint,
		userAgentAutogenDisabled: disableAuto,
		timing:                   opts.TimingOverride,
		requestHeaders:           opts.CustomHeaders,
		requestCookies:           opts.CustomCookies,
		options:                  options.NewManager(options.ManagerOptions{ScriptDirectory: scriptDir}),
		geolocationConfig:        opts.Geolocation,
		commandSecret:            opts.CommandSecret,
	}
	agent.scriptRunner = newScriptRunner(agent, agent.options)

	agent.reloadResultCache()

	verifyOpts := deriveSignatureVerifyOptions(registration.Config, opts.Logger)

	if manager, err := plugins.NewManager(defaultPluginRoot(opts.Preferences), opts.Logger, verifyOpts); err != nil {
		opts.Logger.Printf("plugin telemetry disabled: %v", err)
	} else {
		agent.plugins = manager
	}

	if agent.plugins != nil {
		if err := agent.refreshApprovedPlugins(ctx); err != nil && opts.Logger != nil {
			opts.Logger.Printf("plugin manifest refresh failed: %v", err)
		}
	}

	if notesPath, err := notes.DefaultPath(dataDirectory(opts.Preferences)); err != nil {
		opts.Logger.Printf("notes disabled (path error): %v", err)
	} else {
		sharedMaterial := opts.SharedSecret
		if strings.TrimSpace(sharedMaterial) == "" {
			sharedMaterial = registration.AgentKey + "-shared"
		}
		localMaterial := opts.SharedSecret
		if strings.TrimSpace(localMaterial) == "" {
			localMaterial = registration.AgentID
		}
		if notesManager, err := notes.NewManager(notesPath, localMaterial, sharedMaterial, registration.AgentKey); err != nil {
			opts.Logger.Printf("notes disabled (init failed): %v", err)
		} else {
			agent.notes = notesManager
		}
	}

	modules := newDefaultModuleManager()
	modules.SetEnabledModules(opts.EnabledModules)
	agent.modules = modules
	if err := modules.Init(ctx, agent.moduleRuntime()); err != nil {
		return fmt.Errorf("initialize modules: %w", err)
	}

	router, err := newDefaultCommandRouter()
	if err != nil {
		return fmt.Errorf("initialize commands: %w", err)
	}
	agent.commands = router

	agent.applyPreferences()

	opts.Logger.Printf("registered as %s", agent.id)
	agent.processCommands(ctx, registration.Commands)

	go agent.runCommandStream(ctx)
	go agent.monitorScriptAutomation(ctx)
	go agent.monitorOptionsAutomation(ctx)
	go agent.run(ctx)

	<-ctx.Done()
	opts.Logger.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), opts.ShutdownGrace)
	defer cancel()
	agent.shutdown(shutdownCtx)

	return nil
}

func (o RuntimeOptions) maxBackoffOverride() time.Duration {
	if o.TimingOverride.MaxBackoff > 0 {
		return o.TimingOverride.MaxBackoff
	}
	return 0
}

func canonicalizeServerURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("server url must be provided")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid server url: %w", err)
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid server url: %s", trimmed)
	}

	host := parsed.Hostname()
	port := parsed.Port()

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "https", "http":
		// allowed
	default:
		return "", fmt.Errorf("unsupported server url scheme: %s", parsed.Scheme)
	}

	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}

	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else {
		if strings.Contains(host, ":") {
			parsed.Host = "[" + host + "]"
		} else {
			parsed.Host = host
		}
	}

	return parsed.String(), nil
}

func deriveSignatureVerifyOptions(cfg protocol.AgentConfig, logger *log.Logger) manifest.VerifyOptions {
	opts := plugins.BuiltInVerifyOptions()

	if cfg.Plugins == nil || cfg.Plugins.SignaturePolicy == nil {
		return opts
	}

	policy := cfg.Plugins.SignaturePolicy

	if len(policy.SHA256AllowList) > 0 {
		for _, value := range policy.SHA256AllowList {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			opts.SHA256AllowList = append(opts.SHA256AllowList, strings.ToLower(trimmed))
		}
	}

	if len(policy.Ed25519PublicKeys) > 0 {
		if opts.Ed25519PublicKeys == nil {
			opts.Ed25519PublicKeys = make(map[string]ed25519.PublicKey)
		}
		for keyID, encoded := range policy.Ed25519PublicKeys {
			trimmed := strings.TrimSpace(encoded)
			if trimmed == "" {
				continue
			}
			decoded, err := hex.DecodeString(trimmed)
			if err != nil {
				if logger != nil {
					logger.Printf("plugin verifier: invalid public key for %s: %v", keyID, err)
				}
				continue
			}
			if len(decoded) != ed25519.PublicKeySize {
				if logger != nil {
					logger.Printf("plugin verifier: public key %s has invalid length %d", keyID, len(decoded))
				}
				continue
			}
			opts.Ed25519PublicKeys[keyID] = ed25519.PublicKey(append([]byte(nil), decoded...))
		}
	}

	if policy.MaxSignatureAgeMs > 0 {
		opts.MaxSignatureAge = time.Duration(policy.MaxSignatureAgeMs) * time.Millisecond
	}

	return opts
}
