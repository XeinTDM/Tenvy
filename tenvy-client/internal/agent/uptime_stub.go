//go:build !linux && !windows && !darwin

package agent

import (
	"errors"
	"time"
)

func systemUptime() (time.Duration, error) {
	return 0, errors.New("system uptime unsupported on this platform")
}
