//go:build linux

package options

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type linuxPlatformService struct {
	gsettings string
	pactl     string
}

func newPlatformService() PlatformService {
	gsettings, _ := exec.LookPath("gsettings")
	pactl, _ := exec.LookPath("pactl")
	return &linuxPlatformService{gsettings: gsettings, pactl: pactl}
}

func (s *linuxPlatformService) Execute(ctx context.Context, operation string, metadata map[string]any, state State) (string, error) {
	switch operation {
	case "defender-exclusion":
		enabled := false
		if v, ok := metadata["enabled"].(bool); ok {
			enabled = v
		}
		return s.configureClamAVExclusion(enabled)
	case "wallpaper-mode":
		mode, _ := metadata["mode"].(string)
		return s.configureWallpaper(ctx, strings.TrimSpace(mode))
	case "visual-distortion":
		mode, _ := metadata["mode"].(string)
		trimmed := strings.TrimSpace(mode)
		if trimmed == "" {
			trimmed = "unspecified"
		}
		return fmt.Sprintf("Visual distortion %s unsupported on Linux", trimmed), nil
	case "cursor-behavior":
		behavior, _ := metadata["behavior"].(string)
		trimmed := strings.TrimSpace(behavior)
		if trimmed == "" {
			trimmed = "unspecified"
		}
		return fmt.Sprintf("Cursor behavior %s unsupported on Linux", trimmed), nil
	case "fake-event-mode":
		mode, _ := metadata["mode"].(string)
		trimmed := strings.TrimSpace(mode)

		if strings.EqualFold(trimmed, "fakeerror") {
			if path, err := exec.LookPath("zenity"); err == nil {
				go exec.CommandContext(context.Background(), path, "--error", "--text=A critical system error has occurred. Memory at 0x00401000 could not be read.", "--title=System Error", "--width=400").Run()
				return "Fake error message displayed (zenity)", nil
			}
			if path, err := exec.LookPath("kdialog"); err == nil {
				go exec.CommandContext(context.Background(), path, "--error", "A critical system error has occurred. Memory at 0x00401000 could not be read.", "--title=System Error").Run()
				return "Fake error message displayed (kdialog)", nil
			}
		}

		if trimmed == "" || strings.EqualFold(trimmed, "none") {
			return "Fake event mode cleared (no native integration on Linux)", nil
		}
		return fmt.Sprintf("Fake event mode %s unsupported on Linux", trimmed), nil
	case "sound-playback":
		enabled := state.SoundPlayback
		if v, ok := metadata["enabled"].(bool); ok {
			enabled = v
		}
		return s.configureSoundPlayback(ctx, enabled)
	case "sound-volume":
		volume := state.SoundVolume
		if raw, ok := metadata["volume"]; ok {
			switch v := raw.(type) {
			case int:
				volume = v
			case int64:
				volume = int(v)
			case float64:
				volume = int(v)
			}
		}
		if volume < 0 {
			volume = 0
		}
		if volume > 100 {
			volume = 100
		}
		return s.configureSoundVolume(ctx, volume)
	default:
		return "", nil
	}
}

func (s *linuxPlatformService) configureClamAVExclusion(enabled bool) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user directory: %w", err)
	}
	configDir := filepath.Join(home, ".config", "tenvy")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", fmt.Errorf("prepare exclusion directory: %w", err)
	}
	exclusionFile := filepath.Join(configDir, "clamav-exclusions.conf")
	contents := ""
	if data, err := os.ReadFile(exclusionFile); err == nil {
		contents = string(data)
	}
	lines := strings.Split(contents, "\n")
	normalized := filepath.Clean(executable)
	if enabled {
		already := false
		for _, line := range lines {
			if strings.TrimSpace(line) == normalized {
				already = true
				break
			}
		}
		if !already {
			lines = append(lines, normalized)
		}
		final := strings.TrimSpace(strings.Join(lines, "\n"))
		if final != "" {
			final += "\n"
		}
		if err := os.WriteFile(exclusionFile, []byte(final), 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("Recorded ClamAV exclusion for %s", normalized), nil
	}
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimSpace(line) == normalized {
			continue
		}
		filtered = append(filtered, line)
	}
	final := strings.TrimSpace(strings.Join(filtered, "\n"))
	if final != "" {
		final += "\n"
	}
	if err := os.WriteFile(exclusionFile, []byte(final), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed ClamAV exclusion for %s", normalized), nil
}

func (s *linuxPlatformService) configureWallpaper(ctx context.Context, mode string) (string, error) {
	if s.gsettings == "" || mode == "" {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, s.gsettings, "set", "org.gnome.desktop.background", "picture-options", mode)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("update wallpaper mode: %v", strings.TrimSpace(string(output)))
	}
	return fmt.Sprintf("Set GNOME wallpaper mode to %s", mode), nil
}

func (s *linuxPlatformService) configureSoundPlayback(ctx context.Context, enabled bool) (string, error) {
	if s.pactl == "" {
		return "", nil
	}
	muteArg := "1"
	summary := "Muted system playback"
	if enabled {
		muteArg = "0"
		summary = "Unmuted system playback"
	}
	cmd := exec.CommandContext(ctx, s.pactl, "set-sink-mute", "@DEFAULT_SINK@", muteArg)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("update playback state: %v", strings.TrimSpace(string(output)))
	}
	return summary, nil
}

func (s *linuxPlatformService) configureSoundVolume(ctx context.Context, volume int) (string, error) {
	if s.pactl == "" {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, s.pactl, "set-sink-volume", "@DEFAULT_SINK@", fmt.Sprintf("%d%%", volume))
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("update volume: %v", strings.TrimSpace(string(output)))
	}
	return fmt.Sprintf("Set system volume to %d%%", volume), nil
}
