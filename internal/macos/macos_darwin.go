package macos

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static void post_shortcut(CGKeyCode code, CGEventFlags flags) {
	CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStatePrivate);
	CGEventRef down = CGEventCreateKeyboardEvent(src, code, true);
	CGEventRef up = CGEventCreateKeyboardEvent(src, code, false);
	CGEventSetFlags(down, flags);
	CGEventSetFlags(up, flags);
	CGEventPost(kCGHIDEventTap, down);
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(down);
	CFRelease(up);
	if (src) CFRelease(src);
}

// post_modifier taps one modifier key, as a flags-changed press and release.
static void post_modifier(CGKeyCode code, CGEventFlags flags) {
	CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStatePrivate);
	CGEventRef down = CGEventCreateKeyboardEvent(src, code, true);
	CGEventRef up = CGEventCreateKeyboardEvent(src, code, false);
	CGEventSetType(down, kCGEventFlagsChanged);
	CGEventSetType(up, kCGEventFlagsChanged);
	CGEventSetFlags(down, flags | kCGEventFlagMaskNonCoalesced);
	CGEventSetFlags(up, kCGEventFlagMaskNonCoalesced);
	CGEventPost(kCGHIDEventTap, down);
	usleep(30000); // apps that time a modifier tap see a real press
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(down);
	CFRelease(up);
	if (src) CFRelease(src);
}

static int trusted(int prompt) {
	if (!prompt) return AXIsProcessTrusted();
	const void *keys[] = { kAXTrustedCheckOptionPrompt };
	const void *vals[] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	int ok = AXIsProcessTrustedWithOptions(opts);
	CFRelease(opts);
	return ok;
}

// front_app writes the owner of the frontmost normal window into buf. Window
// lists are ordered front to back; layer 0 holds ordinary app windows. It
// needs no permission (window titles would need Screen Recording).
static int front_app(char *buf, int len, int *pid) {
	CFArrayRef list = CGWindowListCopyWindowInfo(
		kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	if (!list) return 0;
	int found = 0;
	for (CFIndex i = 0; i < CFArrayGetCount(list) && !found; i++) {
		CFDictionaryRef w = CFArrayGetValueAtIndex(list, i);
		int layer = -1;
		CFNumberRef ln = CFDictionaryGetValue(w, kCGWindowLayer);
		if (!ln || !CFNumberGetValue(ln, kCFNumberIntType, &layer) || layer != 0) continue;
		CFStringRef owner = CFDictionaryGetValue(w, kCGWindowOwnerName);
		if (!owner || !CFStringGetCString(owner, buf, len, kCFStringEncodingUTF8)) continue;
		CFNumberRef pn = CFDictionaryGetValue(w, kCGWindowOwnerPID);
		if (pn) CFNumberGetValue(pn, kCFNumberIntType, pid);
		found = 1;
	}
	CFRelease(list);
	return found;
}

// app_names writes the owners of ordinary windows on every Space, one per
// line, into buf.
static int app_names(char *buf, int len) {
	CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	if (!list) return 0;
	int used = 0;
	char name[256];
	for (CFIndex i = 0; i < CFArrayGetCount(list); i++) {
		CFDictionaryRef w = CFArrayGetValueAtIndex(list, i);
		int layer = -1;
		CFNumberRef ln = CFDictionaryGetValue(w, kCGWindowLayer);
		if (!ln || !CFNumberGetValue(ln, kCFNumberIntType, &layer) || layer != 0) continue;
		CFStringRef owner = CFDictionaryGetValue(w, kCGWindowOwnerName);
		if (!owner || !CFStringGetCString(owner, name, sizeof name, kCFStringEncodingUTF8)) continue;
		int n = strlen(name);
		if (used + n + 1 >= len) break;
		memcpy(buf + used, name, n);
		buf[used + n] = '\n';
		used += n + 1;
	}
	CFRelease(list);
	buf[used] = 0;
	return used;
}
*/
import "C"

import (
	"errors"
	"os/exec"
	"sort"
	"strings"
	"unsafe"
)

// RunningApps lists the apps that have an ordinary window, sorted.
func RunningApps() []string {
	const size = 1 << 16
	buf := (*C.char)(C.malloc(size))
	defer C.free(unsafe.Pointer(buf))
	C.app_names(buf, size)
	seen := map[string]bool{}
	var out []string
	for _, n := range strings.Split(C.GoString(buf), "\n") {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// FrontApp names the app that receives keystrokes: the one LaunchServices
// marks as in front, which is not always the owner of the frontmost window
// (Finder with no windows open, after a click on the desktop). It falls back
// to the window owner when lsappinfo is unavailable.
func FrontApp() string {
	if name, ok := lsFront(); ok {
		return name
	}
	return frontWindowOwner()
}

func lsFront() (string, bool) {
	asn, err := exec.Command("/usr/bin/lsappinfo", "front").Output()
	if err != nil {
		return "", false
	}
	out, err := exec.Command("/usr/bin/lsappinfo", "info", "-only", "name", strings.TrimSpace(string(asn))).Output()
	if err != nil {
		return "", false
	}
	return parseLSName(string(out))
}

// parseLSName reads the quoted name lsappinfo prints first:
// "Brave Browser" ASN:0x0-0x3d146109: (in front)
func parseLSName(out string) (string, bool) {
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, `"`) {
		return "", false
	}
	end := strings.Index(out[1:], `"`)
	if end <= 0 {
		return "", false
	}
	return out[1 : end+1], true
}

func frontWindowOwner() string {
	buf := (*C.char)(C.malloc(512))
	defer C.free(unsafe.Pointer(buf))
	var pid C.int
	if C.front_app(buf, 512, &pid) == 0 {
		return ""
	}
	return C.GoString(buf)
}

// Trusted reports whether this executable may send input (Accessibility).
func Trusted() bool { return C.trusted(0) != 0 }

// RequestTrust asks macOS to show the Accessibility prompt for this executable.
func RequestTrust() bool { return C.trusted(1) != 0 }

// ErrNotTrusted means Accessibility has not been granted.
var ErrNotTrusted = errors.New("rex-micro needs Accessibility to send shortcuts: System Settings > Privacy & Security > Accessibility")

// Post sends the shortcut to the frontmost app.
func Post(sc Shortcut) error {
	if !Trusted() {
		return ErrNotTrusted
	}
	if sc.Modifier {
		C.post_modifier(C.CGKeyCode(sc.Code), C.CGEventFlags(sc.Flags))
		return nil
	}
	C.post_shortcut(C.CGKeyCode(sc.Code), C.CGEventFlags(sc.Flags))
	return nil
}
