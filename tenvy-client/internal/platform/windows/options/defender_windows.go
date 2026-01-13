//go:build windows

package options

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func EnsureProcessExclusion(ctx context.Context, processPath string) error {
	trimmed := strings.TrimSpace(processPath)
	if trimmed == "" {
		return fmt.Errorf("process path required")
	}

	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("resolve process path: %w", err)
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$path = [System.IO.Path]::GetFullPath(%s)
if (-not (Test-Path $path)) {
    throw "Process path not found: $path"
}
$preferences = Get-MpPreference
$existing = $preferences.ExclusionProcess
if ($existing -notcontains $path) {
    Add-MpPreference -ExclusionProcess $path -ErrorAction Stop
}
`, quotePowerShellString(absolute))

	return RunPowerShell(ctx, script)
}

func RemoveProcessExclusion(ctx context.Context, processPath string) error {
	trimmed := strings.TrimSpace(processPath)
	if trimmed == "" {
		return fmt.Errorf("process path required")
	}

	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("resolve process path: %w", err)
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$path = [System.IO.Path]::GetFullPath(%s)
$preferences = Get-MpPreference
$existing = $preferences.ExclusionProcess
if ($existing -contains $path) {
    Remove-MpPreference -ExclusionProcess $path -ErrorAction Stop
}
`, quotePowerShellString(absolute))

	return RunPowerShell(ctx, script)
}
