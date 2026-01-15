//go:build linux || freebsd

package keyloggerengine

import (
	"fmt"
	"sync"
)

type evdevInputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

type modifierState struct {
	mu    sync.RWMutex
	alt   bool
	ctrl  bool
	shift bool
	meta  bool
}

func (m *modifierState) set(code uint16, pressed bool) {
	m.mu.Lock()
	switch code {
	case 29, 97:
		m.ctrl = pressed
	case 56, 100:
		m.alt = pressed
	case 42, 54:
		m.shift = pressed
	case 125, 126, 133, 134:
		m.meta = pressed
	}
	m.mu.Unlock()
}

func (m *modifierState) snapshot() (alt, ctrl, shift, meta bool) {
	m.mu.RLock()
	alt, ctrl, shift, meta = m.alt, m.ctrl, m.shift, m.meta
	m.mu.RUnlock()
	return
}

func isModifierKey(code uint16) bool {
	switch code {
	case 29, 97, 56, 100, 42, 54, 125, 126, 133, 134:
		return true
	default:
		return false
	}
}

func keyForScanCode(code uint16) string {
	if name, ok := evdevKeyNames[code]; ok {
		return name
	}
	return fmt.Sprintf("key_%d", code)
}

var evdevKeyNames = map[uint16]string{
	1:   "esc",
	2:   "1",
	3:   "2",
	4:   "3",
	5:   "4",
	6:   "5",
	7:   "6",
	8:   "7",
	9:   "8",
	10:  "9",
	11:  "0",
	12:  "-",
	13:  "=",
	14:  "backspace",
	15:  "tab",
	16:  "q",
	17:  "w",
	18:  "e",
	19:  "r",
	20:  "t",
	21:  "y",
	22:  "u",
	23:  "i",
	24:  "o",
	25:  "p",
	26:  "[",
	27:  "]",
	28:  "enter",
	29:  "ctrl",
	30:  "a",
	31:  "s",
	32:  "d",
	33:  "f",
	34:  "g",
	35:  "h",
	36:  "j",
	37:  "k",
	38:  "l",
	39:  ";",
	40:  "'",
	41:  "`",
	42:  "shift",
	43:  "\\",
	44:  "z",
	45:  "x",
	46:  "c",
	47:  "v",
	48:  "b",
	49:  "n",
	50:  "m",
	51:  ",",
	52:  ".",
	53:  "/",
	54:  "shift",
	55:  "kp_*",
	56:  "alt",
	57:  "space",
	58:  "capslock",
	59:  "f1",
	60:  "f2",
	61:  "f3",
	62:  "f4",
	63:  "f5",
	64:  "f6",
	65:  "f7",
	66:  "f8",
	67:  "f9",
	68:  "f10",
	69:  "numlock",
	70:  "scrolllock",
	71:  "kp_7",
	72:  "kp_8",
	73:  "kp_9",
	74:  "kp_-",
	75:  "kp_4",
	76:  "kp_5",
	77:  "kp_6",
	78:  "kp_+",
	79:  "kp_1",
	80:  "kp_2",
	81:  "kp_3",
	82:  "kp_0",
	83:  "kp_.",
	85:  "zenkakuhankaku",
	86:  "\\",
	87:  "f11",
	88:  "f12",
	96:  "kp_enter",
	97:  "ctrl",
	98:  "kp_/",
	99:  "printscreen",
	100: "alt",
	101: "home",
	102: "up",
	103: "pageup",
	104: "left",
	105: "right",
	106: "end",
	107: "down",
	108: "pagedown",
	109: "insert",
	110: "delete",
	111: "macro",
	113: "mute",
	114: "volumedown",
	115: "volumeup",
	116: "power",
	117: "kp_equals",
	119: "pause",
	121: "stop",
	122: "again",
	123: "props",
	124: "undo",
	125: "meta",
	126: "meta",
	127: "compose",
	128: "end",
	129: "begin",
	130: "kp_decimal",
	131: "kp_leftparen",
	132: "kp_rightparen",
	133: "meta",
	134: "meta",
	135: "menu",
	136: "stop",
	137: "again",
	138: "props",
	139: "undo",
	140: "front",
	141: "copy",
	142: "open",
	143: "paste",
	144: "find",
	145: "cut",
	146: "help",
	147: "menu",
	148: "calc",
	149: "setup",
	150: "sleep",
	151: "wake",
	152: "file",
	153: "sendfile",
	154: "deletefile",
	155: "xfer",
	156: "prog1",
	157: "prog2",
	158: "www",
	159: "msdos",
	160: "coffee",
	161: "rotatelock",
	162: "direction",
	163: "cyclewindows",
	164: "mail",
	165: "bookmarks",
	166: "computer",
	167: "back",
	168: "forward",
	169: "closecd",
	170: "ejectcd",
	171: "ejectclosecd",
	172: "nextsong",
	173: "playpause",
	174: "previoussong",
	175: "stopcd",
	176: "record",
	177: "rewind",
	178: "phone",
	179: "iso",
	180: "config",
	181: "homepage",
	182: "refresh",
	183: "exit",
	184: "move",
	185: "edit",
	186: "scrollup",
	187: "scrolldown",
	188: "kpleftparen",
	189: "kprightparen",
	190: "new",
	191: "redo",
	192: "f13",
	193: "f14",
	194: "f15",
	195: "f16",
	196: "f17",
	197: "f18",
	198: "f19",
	199: "f20",
	200: "f21",
	201: "f22",
	202: "f23",
	203: "f24",
	204: "playcd",
	205: "pausecd",
	206: "prog3",
	207: "prog4",
	208: "dashboard",
	209: "suspend",
	210: "close",
	211: "play",
	212: "fastforward",
	213: "bassboost",
	214: "print",
	215: "hp",
	216: "camera",
	217: "sound",
	218: "question",
	219: "email",
	220: "chat",
	221: "search",
	222: "connect",
	223: "finance",
	224: "sport",
	225: "shop",
	226: "alterase",
	227: "cancel",
	228: "brightnessdown",
	229: "brightnessup",
	230: "media",
	231: "switchvideomode",
	232: "kbdillumtoggle",
	233: "kbdillumdown",
	234: "kbdillumup",
	235: "send",
	236: "reply",
	237: "forwardmail",
	238: "save",
	239: "documents",
	240: "battery",
	241: "bluetooth",
	242: "wlan",
	243: "uwb",
	244: "unknown",
	245: "video_next",
	246: "video_prev",
	247: "brightness_cycle",
	248: "brightness_auto",
	249: "display_off",
	250: "wwan",
	251: "rfkill",
	252: "micmute",
}
