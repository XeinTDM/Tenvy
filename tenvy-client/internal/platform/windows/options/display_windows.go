//go:build windows

package options

import (
	"context"
	"fmt"
	"strings"
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
	return RunPowerShell(ctx, script)
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
	return RunPowerShell(ctx, script)
}

func ConfigureScreenOrientation(ctx context.Context, orientation string) error {
	var value int
	switch strings.ToLower(strings.TrimSpace(orientation)) {
	case "normal":
		value = 0
	case "rotateleft":
		value = 1
	case "upsidedown":
		value = 2
	case "rotateright":
		value = 3
	default:
		return fmt.Errorf("unsupported orientation: %s", orientation)
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;

public class DisplayOrientation {
    [DllImport("user32.dll")]
    public static extern int EnumDisplaySettings(string deviceName, int modeNum, ref DEVMODE devMode);

    [DllImport("user32.dll")]
    public static extern int ChangeDisplaySettings(ref DEVMODE devMode, int flags);

    [StructLayout(LayoutKind.Sequential)]
    public struct DEVMODE {
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
        public string dmDeviceName;
        public short dmSpecVersion;
        public short dmDriverVersion;
        public short dmSize;
        public short dmDriverExtra;
        public int dmFields;
        public int dmPositionX;
        public int dmPositionY;
        public int dmDisplayOrientation;
        public int dmDisplayFixedOutput;
        public short dmColor;
        public short dmDuplex;
        public short dmYResolution;
        public short dmTTOption;
        public short dmCollate;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
        public string dmFormName;
        public short dmLogPixels;
        public short dmBitsPerPel;
        public int dmPelsWidth;
        public int dmPelsHeight;
        public int dmDisplayFlags;
        public int dmNup;
        public int dmDisplayFrequency;
        public int dmICMMethod;
        public int dmICMIntent;
        public int dmMediaType;
        public int dmDitherType;
        public int dmReserved1;
        public int dmReserved2;
        public int dmPanningWidth;
        public int dmPanningHeight;
    }
}
"@

$devMode = New-Object DisplayOrientation+DEVMODE
$devMode.dmSize = [System.Runtime.InteropServices.Marshal]::SizeOf($devMode)

if ([DisplayOrientation]::EnumDisplaySettings($null, -1, [ref]$devMode) -ne 0) {
    if ($devMode.dmDisplayOrientation -ne %d) {
        $oldOrientation = $devMode.dmDisplayOrientation
        $newOrientation = %d
        
        # If we are rotating 90 or 270 degrees from 0 or 180, swap width and height
        # This is a bit simplistic but works for most primary displays
        $isOldPortrait = ($oldOrientation -eq 1 -or $oldOrientation -eq 3)
        $isNewPortrait = ($newOrientation -eq 1 -or $newOrientation -eq 3)
        
        if ($isOldPortrait -ne $isNewPortrait) {
            $tmp = $devMode.dmPelsWidth
            $devMode.dmPelsWidth = $devMode.dmPelsHeight
            $devMode.dmPelsHeight = $tmp
        }
        
        $devMode.dmDisplayOrientation = $newOrientation
        $devMode.dmFields = 0x00000080 -bor 0x00040000 -bor 0x00080000 # DM_DISPLAYORIENTATION | DM_PELSWIDTH | DM_PELSHEIGHT
        $result = [DisplayOrientation]::ChangeDisplaySettings([ref]$devMode, 0)
        if ($result -ne 0) {
            throw "ChangeDisplaySettings failed with code $result"
        }
    }
} else {
    throw "EnumDisplaySettings failed"
}
`, value, value)
	return RunPowerShell(ctx, script)
}

func ConfigureWallpaper(ctx context.Context, mode string) error {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	var script string

	switch normalized {
	case "default":
		return nil
	case "black":
		script = `
$ErrorActionPreference = 'Stop'
$code = @"
using System.Runtime.InteropServices;
public class Wallpaper {
    [DllImport("user32.dll", CharSet = CharSet.Auto)]
    public static extern int SystemParametersInfo(int uAction, int uParam, string lpvParam, int fuWinIni);
}
"@
Add-Type -TypeDefinition $code
# Set wallpaper to empty string to use solid color
[Wallpaper]::SystemParametersInfo(20, 0, "", 3)
Set-ItemProperty -Path 'HKCU:\Control Panel\Colors' -Name 'Background' -Value '0 0 0'
& $env:SystemRoot\System32\rundll32.exe user32.dll,UpdatePerUserSystemParameters
`
	case "random":
		script = `
$ErrorActionPreference = 'Stop'
$code = @"
using System.Runtime.InteropServices;
public class Wallpaper {
    [DllImport("user32.dll", CharSet = CharSet.Auto)]
    public static extern int SystemParametersInfo(int uAction, int uParam, string lpvParam, int fuWinIni);
}
"@
Add-Type -TypeDefinition $code
$colors = @('255 0 0', '0 255 0', '0 0 255', '255 255 0', '255 0 255', '0 255 255')
$color = $colors | Get-Random
[Wallpaper]::SystemParametersInfo(20, 0, "", 3)
Set-ItemProperty -Path 'HKCU:\Control Panel\Colors' -Name 'Background' -Value $color
& $env:SystemRoot\System32\rundll32.exe user32.dll,UpdatePerUserSystemParameters
`
	default:
		return fmt.Errorf("unsupported wallpaper mode: %s", mode)
	}

	return RunPowerShell(ctx, script)
}
