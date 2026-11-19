package macos

/*
#cgo LDFLAGS: -framework CoreServices -framework CoreFoundation
#include <CoreServices/CoreServices.h>
#include <libproc.h>
#include <stdlib.h>

static int pid_path(int pid, char *buf, int size) {
	return proc_pidpath(pid, buf, (uint32_t)size);
}

// app_paths writes the paths of every installed app with the bundle ID,
// separated by newlines, into buf. It returns the bytes written, or -1.
static int app_paths(const char *bundle_id, char *buf, int size) {
	CFStringRef id = CFStringCreateWithCString(NULL, bundle_id, kCFStringEncodingUTF8);
	if (!id) return -1;
	CFArrayRef urls = LSCopyApplicationURLsForBundleIdentifier(id, NULL);
	CFRelease(id);
	if (!urls) return 0;
	int n = 0;
	for (CFIndex i = 0; i < CFArrayGetCount(urls); i++) {
		CFURLRef url = (CFURLRef)CFArrayGetValueAtIndex(urls, i);
		char path[PATH_MAX];
		if (!CFURLGetFileSystemRepresentation(url, true, (UInt8 *)path, sizeof path)) continue;
		int len = (int)strlen(path);
		if (n + len + 1 >= size) break;
		memcpy(buf + n, path, len);
		n += len;
		buf[n++] = '\n';
	}
	CFRelease(urls);
	return n;
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

// ProcessPath returns the executable a running process was started from.
func ProcessPath(pid int) (string, error) {
	buf := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
	n := C.pid_path(C.int(pid), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if n <= 0 {
		return "", fmt.Errorf("no executable path for pid %d", pid)
	}
	return string(buf[:n]), nil
}

// AppPaths lists the installed apps with a bundle ID, as LaunchServices
// knows them, wherever they live; the preferred one comes first.
func AppPaths(bundleID string) []string {
	cs := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cs))
	buf := make([]byte, 16*1024)
	n := C.app_paths(cs, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if n <= 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(buf[:n]), "\n"), "\n")
}
