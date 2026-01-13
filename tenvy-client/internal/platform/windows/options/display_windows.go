//go:build windows

package options

import (
	"context"
	"fmt"
)

func ConfigureColorFilter(ctx context.Context, active bool, filterType int) error {
	value := 0
	if active {
		value = 1
	}
	if filterType < 0 {
		filterType = 0
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$base = 'HKCU:\Software\Microsoft\ColorFiltering'
if (-not (Test-Path $base)) {
    New-Item -Path $base -Force | Out-Null
}
New-ItemProperty -Path $base -Name 'Active' -PropertyType DWord -Value %d -Force | Out-Null
New-ItemProperty -Path $base -Name 'ColorFilterHotkeyEnabled' -PropertyType DWord -Value 0 -Force | Out-Null
New-ItemProperty -Path $base -Name 'FilterType' -PropertyType DWord -Value %d -Force | Out-Null
`, value, filterType)
	return runPowerShell(ctx, script)
}

func ConfigureCursorState(ctx context.Context, swapButtons bool, trails int) error {
	if trails < 0 {
		trails = 0
	}
	if trails > 10 {
		trails = 10
	}
	swapValue := "0"
	if swapButtons {
		swapValue = "1"
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$mousePath = 'HKCU:\Control Panel\Mouse'
if (-not (Test-Path $mousePath)) {
    New-Item -Path $mousePath -Force | Out-Null
}
New-ItemProperty -Path $mousePath -Name 'SwapMouseButtons' -PropertyType String -Value '%s' -Force | Out-Null
New-ItemProperty -Path $mousePath -Name 'MouseTrails' -PropertyType String -Value '%d' -Force | Out-Null
& $env:SystemRoot\System32\rundll32.exe user32.dll,UpdatePerUserSystemParameters
`, swapValue, trails)
	return runPowerShell(ctx, script)
}
