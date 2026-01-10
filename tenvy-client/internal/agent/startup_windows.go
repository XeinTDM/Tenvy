//go:build windows

package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/rootbay/tenvy-client/internal/platform"
	"golang.org/x/sys/windows/registry"
)

const (
	windowsRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func registerStartup(target string, branding PersistenceBranding) error {
	if redirect := os.Getenv("TENVY_WINDOWS_RUN_FILE"); redirect != "" {
		return os.WriteFile(redirect, []byte(target), 0o644)
	}

	root := registry.CURRENT_USER
	if platform.CurrentUserIsElevated() {
		root = registry.LOCAL_MACHINE
	}

	key, _, err := registry.CreateKey(root, windowsRunKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open run key: %w", err)
	}
	defer key.Close()

	valueName := strings.TrimSpace(branding.RunKeyName)
	if valueName == "" {
		valueName = "TenvyAgent"
	}

	if err := key.SetStringValue(valueName, fmt.Sprintf("\"%s\"", target)); err != nil {
		return fmt.Errorf("set run value: %w", err)
	}

	return nil
}

func unregisterStartup(branding PersistenceBranding) error {
	if redirect := os.Getenv("TENVY_WINDOWS_RUN_FILE"); redirect != "" {
		if err := os.Remove(redirect); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove redirected run file: %w", err)
		}
		return nil
	}

	// Try to remove from both if we have privileges, otherwise just CURRENT_USER
	roots := []registry.Key{registry.CURRENT_USER}
	if platform.CurrentUserIsElevated() {
		roots = append(roots, registry.LOCAL_MACHINE)
	}

	for _, root := range roots {
		key, err := registry.OpenKey(root, windowsRunKey, registry.SET_VALUE)
		if err != nil {
			continue
		}
		
		valueName := strings.TrimSpace(branding.RunKeyName)
		if valueName == "" {
			valueName = "TenvyAgent"
		}

		_ = key.DeleteValue(valueName)
		key.Close()
	}

	return nil
}
