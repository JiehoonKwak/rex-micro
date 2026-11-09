// Package macos holds the few macOS services rex-micro needs beyond the pad:
// which app is in front, and sending a keyboard shortcut to it.
package macos

import (
	"fmt"
	"strings"
)

// Shortcut is a parsed key combination such as cmd+shift+], or one modifier
// key pressed alone (right_option), which dictation apps use as a hotkey.
type Shortcut struct {
	Code     uint16 // macOS virtual key code (ANSI layout)
	Flags    uint64 // CGEventFlags modifier mask, held while the key is down
	Modifier bool   // Code is the modifier key itself, tapped alone
}

const (
	flagShift   = 0x20000
	flagControl = 0x40000
	flagOption  = 0x80000
	flagCommand = 0x100000
	flagFn      = 0x800000
)

var modifiers = map[string]uint64{
	"cmd": flagCommand, "command": flagCommand,
	"ctrl": flagControl, "control": flagControl,
	"alt": flagOption, "opt": flagOption, "option": flagOption,
	"shift": flagShift, "fn": flagFn,
}

// lone are modifier keys a shortcut may press by themselves. Flags include
// the device bit for the side, as a physical key press reports it.
var lone = map[string]Shortcut{
	"cmd": {55, flagCommand | 0x8, true}, "command": {55, flagCommand | 0x8, true},
	"right_cmd": {54, flagCommand | 0x10, true}, "right_command": {54, flagCommand | 0x10, true},
	"shift": {56, flagShift | 0x2, true}, "right_shift": {60, flagShift | 0x4, true},
	"alt": {58, flagOption | 0x20, true}, "opt": {58, flagOption | 0x20, true}, "option": {58, flagOption | 0x20, true},
	"right_alt": {61, flagOption | 0x40, true}, "right_opt": {61, flagOption | 0x40, true}, "right_option": {61, flagOption | 0x40, true},
	"ctrl": {59, flagControl | 0x1, true}, "control": {59, flagControl | 0x1, true},
	"right_ctrl": {62, flagControl | 0x2000, true}, "right_control": {62, flagControl | 0x2000, true},
	"fn": {63, flagFn, true},
}

// keyCodes are positions on the ANSI keyboard; a shortcut names the key, not
// the character it types, so cmd+shift+] is the key that also types }.
var keyCodes = map[string]uint16{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
	"b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
	"1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "=": 24, "9": 25, "7": 26,
	"-": 27, "8": 28, "0": 29, "]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35,
	"enter": 36, "return": 36, "l": 37, "j": 38, "'": 39, "k": 40, ";": 41, "\\": 42,
	",": 43, "/": 44, "n": 45, "m": 46, ".": 47, "tab": 48, "space": 49, "`": 50,
	"backspace": 51, "delete": 51, "escape": 53, "esc": 53,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
	"f9": 101, "f10": 109, "f11": 103, "f12": 111, "f13": 105, "f14": 107, "f15": 113,
	"f16": 106, "f17": 64, "f18": 79, "f19": 80, "f20": 90,
	"home": 115, "page_up": 116, "forward_delete": 117, "end": 119, "page_down": 121,
	"left": 123, "right": 124, "down": 125, "up": 126,
}

// Fields splits shortcut text into the parts it presses, trimmed and lower
// case: modifiers, then one key. A trailing "++" is the + key itself, which
// is shift and =, so "cmd++" is cmd, shift, =. Everything that reads a
// shortcut (posting it, showing it) goes through here.
func Fields(s string) []string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if n := len(parts); n >= 2 && parts[n-1] == "" && parts[n-2] == "" {
		parts = append(parts[:n-2], "shift", "=")
	}
	return parts
}

// ParseShortcut reads "cmd+shift+]" style text: modifiers, then one key.
func ParseShortcut(s string) (Shortcut, error) {
	parts := Fields(s)
	if len(parts) == 1 {
		if sc, ok := lone[parts[0]]; ok {
			return sc, nil
		}
	}
	var sc Shortcut
	for i, p := range parts {
		if i < len(parts)-1 {
			m, ok := modifiers[p]
			if !ok {
				return Shortcut{}, fmt.Errorf("shortcut %q: %q is not a modifier (cmd, ctrl, alt, shift, fn)", s, p)
			}
			sc.Flags |= m
			continue
		}
		code, ok := keyCodes[p]
		if !ok {
			if _, mod := modifiers[p]; mod {
				return Shortcut{}, fmt.Errorf("shortcut %q: ends with a modifier; name a key after it", s)
			}
			return Shortcut{}, fmt.Errorf("shortcut %q: unknown key %q (letters, digits, punctuation, enter, tab, space, escape, backspace, arrows, home, end, page_up, page_down, f1-f20)", s, p)
		}
		sc.Code = code
	}
	return sc, nil
}
