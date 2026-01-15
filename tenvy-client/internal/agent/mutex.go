package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rootbay/tenvy-client/internal/platform"
)

type instanceLock struct {
	file      *os.File
	path      string
	name      string
	recovered bool
}

func (l *instanceLock) Release() {
	if l.file != nil {
		l.file.Close()
	}
	if l.path != "" {
		os.Remove(l.path)
	}
}

func (l *instanceLock) Name() string {
	return l.name
}

func (l *instanceLock) Recovered() bool {
	if l == nil {
		return false
	}
	return l.recovered
}

func acquireInstanceMutex(rawKey string) (*instanceLock, error) {
	key := strings.TrimSpace(rawKey)
	if key == "" {
		return nil, nil
	}

	normalized := strings.ToLower(key)
	hashed := sha256.Sum256([]byte(normalized))
	token := hex.EncodeToString(hashed[:16])
	lockPath := filepath.Join(os.TempDir(), fmt.Sprintf("tenvy-%s.lock", token))

	recovered := false
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			if _, writeErr := file.WriteString(fmt.Sprintf("pid=%d\n", os.Getpid())); writeErr != nil {
				// ignore
			}
			return &instanceLock{file: file, path: lockPath, name: key, recovered: recovered}, nil
		}

		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create mutex %s: %w", key, err)
		}

		stale, staleErr := lockFileIsStale(lockPath)
		if staleErr != nil {
			return nil, fmt.Errorf("inspect mutex %s: %w", key, staleErr)
		}
		if !stale {
			return nil, fmt.Errorf("mutex %s is already acquired", key)
		}

		if removeErr := os.Remove(lockPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("cleanup stale mutex %s: %w", key, removeErr)
		}
		recovered = true
	}

	return nil, fmt.Errorf("mutex %s is already acquired", key)
}

func lockFileIsStale(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	pid := parseLockPID(string(data))
	if pid <= 0 {
		return true, nil
	}

	alive, err := platform.ProcessExists(pid)
	if err != nil {
		return false, err
	}

	return !alive, nil
}

func parseLockPID(content string) int {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "pid=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "pid="))
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			continue
		}
		return pid
	}
	return 0
}
