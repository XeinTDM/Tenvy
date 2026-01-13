//go:build windows

package options

import "context"

func ConfigureAutomaticUpdates(ctx context.Context, enabled bool) error {
	value := "1"
	if enabled {
		value = "0"
	}

	script := "$ErrorActionPreference = 'Stop'\n" +
		"$path = 'HKLM:\\SOFTWARE\\Policies\\Microsoft\\Windows\\WindowsUpdate\\AU'\n" +
		"if (-not (Test-Path $path)) {\n" +
		"    New-Item -Path $path -Force | Out-Null\n" +
		"}\n" +
		"Set-ItemProperty -Path $path -Name 'NoAutoUpdate' -Type DWord -Value " + value + "\n"

	return RunPowerShell(ctx, script)
}
