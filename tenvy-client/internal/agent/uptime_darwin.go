//go:build darwin

package agent

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

var (
	sysctlTimeval = func(name string) (unix.Timeval, error) {
		tv, err := unix.SysctlTimeval(name)
		if err != nil {
			return unix.Timeval{}, err
		}
		return *tv, nil
	}
	timeNow = time.Now
)

func systemUptime() (time.Duration, error) {
	tv, err := sysctlTimeval("kern.boottime")
	if err != nil {
		return 0, err
	}
	boot := time.Unix(int64(tv.Sec), int64(tv.Usec)*1000)
	now := timeNow()
	if now.Before(boot) {
		return 0, errors.New("system boot time is in the future")
	}
	return now.Sub(boot), nil
}
