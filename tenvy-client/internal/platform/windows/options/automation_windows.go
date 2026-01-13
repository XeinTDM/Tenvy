//go:build windows

package options

import (
	"context"
	"fmt"
)

func MinimizeAllWindows(ctx context.Context) error {
	script := `
$ErrorActionPreference = 'Stop'
$shell = New-Object -ComObject Shell.Application
$shell.MinimizeAll()
`
	return RunPowerShell(ctx, script)
}

func SpeakText(ctx context.Context, text string) error {
	if text == "" {
		return nil
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Speech
$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer
$synth.Speak(%s)
`, quotePowerShellString(text))
	return RunPowerShell(ctx, script)
}
