//go:build windows

package options

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"
)

func RunPowerShell(ctx context.Context, script string) error {
	encoded := encodeCommand(script)
	cmd := exec.CommandContext(
		ctx,
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-EncodedCommand",
		encoded,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("powershell: %s", message)
	}
	return nil
}

func encodeCommand(script string) string {
	runes := []rune(script)
	utf16Data := utf16.Encode(runes)
	buf := make([]byte, len(utf16Data)*2)
	for idx, value := range utf16Data {
		buf[idx*2] = byte(value)
		buf[idx*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func quotePowerShellString(value string) string {
	if value == "" {
		return "''"
	}
	escaped := strings.ReplaceAll(value, "'", "''")
	return "'" + escaped + "'"
}
