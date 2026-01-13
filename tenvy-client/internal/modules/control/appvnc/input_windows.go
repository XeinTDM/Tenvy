//go:build windows

package appvnc

import (
	"context"
	"strings"
	"unsafe"

	"github.com/lxn/win"
	winutil "github.com/rootbay/tenvy-client/internal/platform/windows"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

func processAppVncInput(ctx context.Context, session *sessionState, events []protocol.AppVncInputEvent) error {
	if session == nil || len(events) == 0 {
		return nil
	}

	hwnd := winutil.FindWindowForPID(uint32(session.processID))
	
	// If window-specific input is preferred but window not found, 
	// we still try to inject it globally.
	
	var rect win.RECT
	if hwnd != 0 {
		win.GetWindowRect(hwnd, &rect)
	}

	for _, event := range events {
		switch event.Type {
		case "pointer-move":
			if hwnd != 0 {
				x := int32(rect.Left) + int32(event.X)
				y := int32(rect.Top) + int32(event.Y)
				win.SetCursorPos(x, y)
			}
		case "pointer-button":
			injectPointerButton(event)
		case "pointer-scroll":
			injectPointerScroll(event)
		case "key":
			injectKeyEvent(event)
		}
	}

	return nil
}

func injectPointerButton(event protocol.AppVncInputEvent) {
	var flag uint32
	switch event.Button {
	case "left":
		if event.Pressed {
			flag = win.MOUSEEVENTF_LEFTDOWN
		} else {
			flag = win.MOUSEEVENTF_LEFTUP
		}
	case "right":
		if event.Pressed {
			flag = win.MOUSEEVENTF_RIGHTDOWN
		} else {
			flag = win.MOUSEEVENTF_RIGHTUP
		}
	case "middle":
		if event.Pressed {
			flag = win.MOUSEEVENTF_MIDDLEDOWN
		} else {
			flag = win.MOUSEEVENTF_MIDDLEUP
		}
	default:
		return
	}

	input := win.MOUSE_INPUT{
		Type: win.INPUT_MOUSE,
		Mi: win.MOUSEINPUT{
			DwFlags: flag,
		},
	}
	win.SendInput(1, unsafe.Pointer(&input), int32(unsafe.Sizeof(input)))
}

func injectPointerScroll(event protocol.AppVncInputEvent) {
	var flag uint32
	var data uint32
	
	if event.DeltaY != 0 {
		flag = win.MOUSEEVENTF_WHEEL
		data = uint32(int32(event.DeltaY * 120))
	} else if event.DeltaX != 0 {
		flag = win.MOUSEEVENTF_HWHEEL
		data = uint32(int32(event.DeltaX * 120))
	} else {
		return
	}

	input := win.MOUSE_INPUT{
		Type: win.INPUT_MOUSE,
		Mi: win.MOUSEINPUT{
			DwFlags:   flag,
			MouseData: data,
		},
	}
	win.SendInput(1, unsafe.Pointer(&input), int32(unsafe.Sizeof(input)))
}

func injectKeyEvent(event protocol.AppVncInputEvent) {
	vk := uint16(event.KeyCode)
	if vk == 0 && len(event.Key) == 1 {
		vk = uint16(strings.ToUpper(event.Key)[0])
	}
	if vk == 0 {
		return
	}

	flags := uint32(0)
	if !event.Pressed {
		flags |= win.KEYEVENTF_KEYUP
	}
	
	input := win.KEYBD_INPUT{
		Type: win.INPUT_KEYBOARD,
		Ki: win.KEYBDINPUT{
			WVk:     vk,
			DwFlags: flags,
		},
	}
	win.SendInput(1, unsafe.Pointer(&input), int32(unsafe.Sizeof(input)))
}

func processGlobalInput(events []protocol.AppVncInputEvent) error {
	for _, event := range events {
		switch event.Type {
		case "pointer-button":
			injectPointerButton(event)
		case "pointer-scroll":
			injectPointerScroll(event)
		case "key":
			injectKeyEvent(event)
		}
	}
	return nil
}