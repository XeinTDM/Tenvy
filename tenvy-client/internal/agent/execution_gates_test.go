package agent

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"
)

type stubGateEnvironment struct {
	now             time.Time
	sleeps          []time.Duration
	uptime          time.Duration
	username        string
	locale          string
	internetAddress string
	waitErr         error
	analysis        bool
}

func (s *stubGateEnvironment) Now() time.Time {
	return s.now
}

func (s *stubGateEnvironment) Sleep(ctx context.Context, d time.Duration) error {
	s.sleeps = append(s.sleeps, d)
	s.now = s.now.Add(d)
	return nil
}

func (s *stubGateEnvironment) SystemUptime() (time.Duration, error) {
	return s.uptime, nil
}

func (s *stubGateEnvironment) Username() string {
	return s.username
}

func (s *stubGateEnvironment) Locale() string {
	return s.locale
}

func (s *stubGateEnvironment) WaitForInternet(ctx context.Context, address string) error {
	s.internetAddress = address
	return s.waitErr
}

func (s *stubGateEnvironment) IsAnalysisDetected() bool {
	return s.analysis
}

func TestEnforceExecutionGatesWithEnvWaitsForDelayAndUptime(t *testing.T) {
	t.Parallel()

	env := &stubGateEnvironment{
		now:      time.Unix(0, 0),
		uptime:   5 * time.Minute,
		username: "alice",
		locale:   "en-us",
	}

	gates := ExecutionGates{
		Enabled:          true,
		Delay:            3 * time.Second,
		MinUptime:        15 * time.Minute,
		AllowedUsernames: []string{"alice"},
		AllowedLocales:   []string{"en-us"},
		RequireInternet:  true,
	}

	if err := enforceExecutionGatesWithEnv(context.Background(), env, gates, "https://example.com:8443", log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("expected gates to pass, got %v", err)
	}

	if len(env.sleeps) < 2 {
		t.Fatalf("expected at least two sleep intervals, got %d", len(env.sleeps))
	}
	if env.sleeps[0] != 3*time.Second {
		t.Fatalf("expected initial delay of 3s, got %s", env.sleeps[0])
	}
	if env.sleeps[1] != 10*time.Minute {
		t.Fatalf("expected uptime wait of 10m, got %s", env.sleeps[1])
	}
	if env.internetAddress != "example.com:8443" {
		t.Fatalf("unexpected internet address: %s", env.internetAddress)
	}
}

func TestEnforceExecutionGatesBlocksMismatchedUsername(t *testing.T) {
	t.Parallel()

	env := &stubGateEnvironment{
		now:      time.Now(),
		uptime:   time.Hour,
		username: "mallory",
		locale:   "en-us",
	}

	gates := ExecutionGates{
		Enabled:          true,
		AllowedUsernames: []string{"alice"},
	}

	err := enforceExecutionGatesWithEnv(context.Background(), env, gates, "https://controller", log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatalf("expected username gate to fail")
	}
}

func TestEnforceExecutionGatesPropagatesInternetErrors(t *testing.T) {
	t.Parallel()

	env := &stubGateEnvironment{
		now:      time.Now(),
		uptime:   time.Hour,
		username: "alice",
		locale:   "en-us",
		waitErr:  errors.New("offline"),
	}

	gates := ExecutionGates{
		Enabled:         true,
		RequireInternet: true,
	}

	err := enforceExecutionGatesWithEnv(context.Background(), env, gates, "https://example.com", log.New(io.Discard, "", 0))
	if !errors.Is(err, env.waitErr) {
		t.Fatalf("expected connectivity error, got %v", err)
	}
}

func TestEnforceExecutionGatesBlocksAnalysisStealthily(t *testing.T) {
	t.Parallel()

	env := &stubGateEnvironment{
		now:      time.Now(),
		analysis: true,
	}

	gates := ExecutionGates{
		Enabled:           true,
		RequireNoAnalysis: true,
	}

	err := enforceExecutionGatesWithEnv(context.Background(), env, gates, "https://controller", log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatalf("expected analysis gate to fail")
	}

	expectedErr := "the service did not respond to the start or control request in a timely fashion"
	if err.Error() != expectedErr {
		t.Fatalf("expected stealthy error %q, got %q", expectedErr, err.Error())
	}

	if len(env.sleeps) != 1 || env.sleeps[0] != time.Hour {
		t.Fatalf("expected 1 hour sleep, got %v", env.sleeps)
	}
}
