//go:build darwin

package hid

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/hid/IOHIDManager.h>
#include <IOKit/hid/IOHIDLib.h>
#include <IOKit/hidsystem/IOHIDLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdint.h>

extern void goHIDMatched(uintptr_t h, uintptr_t dev, int ble);
extern void goHIDRemoved(uintptr_t h, uintptr_t dev);
extern void goHIDReport(uintptr_t h, uintptr_t dev, uint8_t *data, long len);
extern void goHIDOpened(uintptr_t h, int result);

static void onMatched(void *ctx, IOReturn result, void *sender, IOHIDDeviceRef dev) {
	CFStringRef t = IOHIDDeviceGetProperty(dev, CFSTR(kIOHIDTransportKey));
	int ble = 0;
	if (t != NULL && CFGetTypeID(t) == CFStringGetTypeID()) {
		ble = CFStringFind(t, CFSTR("Bluetooth"), 0).location != kCFNotFound;
	}
	CFRetain(dev);
	goHIDMatched((uintptr_t)ctx, (uintptr_t)dev, ble);
}

static void onRemoved(void *ctx, IOReturn result, void *sender, IOHIDDeviceRef dev) {
	goHIDRemoved((uintptr_t)ctx, (uintptr_t)dev);
}

static void onReport(void *ctx, IOReturn result, void *sender, IOHIDReportType type,
                     uint32_t reportID, uint8_t *report, CFIndex length) {
	if (reportID != 6) return;
	goHIDReport((uintptr_t)ctx, (uintptr_t)sender, report, (long)length);
}

static void setInt(CFMutableDictionaryRef d, CFStringRef key, int v) {
	CFNumberRef n = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &v);
	CFDictionarySetValue(d, key, n);
	CFRelease(n);
}

typedef struct {
	IOHIDManagerRef mgr;
	CFRunLoopRef loop;
} hidCtx;

// hidRun creates the manager, schedules it on this thread's run loop and runs
// the loop until hidStop. It returns the IOHIDManagerOpen result if opening fails.
static IOReturn hidRun(hidCtx *c, uintptr_t h, int vid, int pid) {
	c->loop = CFRunLoopGetCurrent();
	CFRetain(c->loop);
	c->mgr = IOHIDManagerCreate(kCFAllocatorDefault, kIOHIDOptionsTypeNone);
	CFMutableDictionaryRef m = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	setInt(m, CFSTR(kIOHIDVendorIDKey), vid);
	setInt(m, CFSTR(kIOHIDProductIDKey), pid);
	IOHIDManagerSetDeviceMatching(c->mgr, m);
	CFRelease(m);
	IOHIDManagerRegisterDeviceMatchingCallback(c->mgr, onMatched, (void *)h);
	IOHIDManagerRegisterDeviceRemovalCallback(c->mgr, onRemoved, (void *)h);
	IOHIDManagerRegisterInputReportCallback(c->mgr, onReport, (void *)h);
	IOHIDManagerScheduleWithRunLoop(c->mgr, c->loop, kCFRunLoopDefaultMode);
	IOReturn r = IOHIDManagerOpen(c->mgr, kIOHIDOptionsTypeNone);
	goHIDOpened(h, (int)r);
	if (r != kIOReturnSuccess) {
		IOHIDManagerUnscheduleFromRunLoop(c->mgr, c->loop, kCFRunLoopDefaultMode);
		CFRelease(c->mgr);
		c->mgr = NULL;
		return r;
	}
	CFRunLoopRun();
	IOHIDManagerRegisterInputReportCallback(c->mgr, NULL, NULL);
	IOHIDManagerRegisterDeviceMatchingCallback(c->mgr, NULL, NULL);
	IOHIDManagerRegisterDeviceRemovalCallback(c->mgr, NULL, NULL);
	IOHIDManagerUnscheduleFromRunLoop(c->mgr, c->loop, kCFRunLoopDefaultMode);
	IOHIDManagerClose(c->mgr, kIOHIDOptionsTypeNone);
	CFRelease(c->mgr);
	c->mgr = NULL;
	return kIOReturnSuccess;
}

static void hidStop(hidCtx *c) {
	if (c->loop != NULL) CFRunLoopStop(c->loop);
}

static IOReturn hidWrite(uintptr_t dev, uint8_t *data, long len) {
	return IOHIDDeviceSetReport((IOHIDDeviceRef)dev, kIOHIDReportTypeOutput, 6, data, len);
}

static void hidRelease(uintptr_t dev) { CFRelease((IOHIDDeviceRef)dev); }
static void hidRetain(uintptr_t dev) { CFRetain((IOHIDDeviceRef)dev); }

static int hidCheckAccess(void) { return (int)IOHIDCheckAccess(kIOHIDRequestTypeListenEvent); }
static int hidRequestAccess(void) { return IOHIDRequestAccess(kIOHIDRequestTypeListenEvent) ? 1 : 0; }
*/
import "C"

import (
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"unsafe"
)

// CheckAccess reports whether this process may read the pad (Input Monitoring).
func CheckAccess() Access {
	switch C.hidCheckAccess() {
	case 0: // kIOHIDAccessTypeGranted
		return AccessGranted
	case 1: // kIOHIDAccessTypeDenied
		return AccessDenied
	}
	return AccessUnknown
}

// RequestAccess asks macOS for Input Monitoring, showing the system prompt the
// first time. It returns true when access is granted.
func RequestAccess() bool { return C.hidRequestAccess() == 1 }

type padRef struct {
	ref uintptr
	ble bool
}

type darwinDevice struct {
	events chan Event
	handle cgo.Handle
	ctx    *C.hidCtx

	mu      sync.Mutex
	sendMu  sync.Mutex
	pads    []padRef
	current *padRef
	closed  bool
	done    chan struct{}
	started chan error
}

// Open starts watching for the pad. It succeeds even when no pad is plugged
// in; a Connected event arrives when one appears.
func Open() (Device, error) {
	d := &darwinDevice{
		events: make(chan Event, 256),
		ctx:    (*C.hidCtx)(C.calloc(1, C.size_t(unsafe.Sizeof(C.hidCtx{})))),
		done:   make(chan struct{}),
	}
	d.handle = cgo.NewHandle(d)
	d.started = make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(d.done)
		C.hidRun(d.ctx, C.uintptr_t(d.handle), VendorID, ProductID)
	}()
	if err := <-d.started; err != nil {
		<-d.done
		d.handle.Delete()
		C.free(unsafe.Pointer(d.ctx))
		return nil, err
	}
	return d, nil
}

func (d *darwinDevice) Events() <-chan Event { return d.events }

func (d *darwinDevice) Transport() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return transportOf(d.current)
}

func transportOf(p *padRef) string {
	switch {
	case p == nil:
		return ""
	case p.ble:
		return TransportBLE
	}
	return TransportUSB
}

func (d *darwinDevice) Send(frame func(transport string) [][]byte) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	d.mu.Lock()
	cur := d.current
	if cur == nil || d.closed {
		d.mu.Unlock()
		return ErrNotConnected
	}
	// Retain under the lock: a removal callback may release the pad's own
	// reference while this write is in flight.
	C.hidRetain(C.uintptr_t(cur.ref))
	d.mu.Unlock()
	defer C.hidRelease(C.uintptr_t(cur.ref))
	for _, report := range frame(transportOf(cur)) {
		buf := C.CBytes(report)
		r := C.hidWrite(C.uintptr_t(cur.ref), (*C.uint8_t)(buf), C.long(len(report)))
		C.free(buf)
		if r != 0 {
			return fmt.Errorf("IOHIDDeviceSetReport failed: 0x%08X", uint32(r))
		}
	}
	return nil
}

func (d *darwinDevice) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()
	C.hidStop(d.ctx)
	<-d.done
	d.mu.Lock()
	for _, p := range d.pads {
		C.hidRelease(C.uintptr_t(p.ref))
	}
	d.pads, d.current = nil, nil
	d.mu.Unlock()
	d.handle.Delete()
	C.free(unsafe.Pointer(d.ctx))
	close(d.events)
	return nil
}

// pick prefers USB over Bluetooth: a cable does not drop.
func (d *darwinDevice) pick() *padRef {
	var best *padRef
	for i := range d.pads {
		p := d.pads[i]
		if best == nil || (best.ble && !p.ble) {
			best = &p
		}
	}
	return best
}

func (d *darwinDevice) emit(e Event) {
	select {
	case d.events <- e:
	default: // never block the run loop; a slow consumer drops reports
	}
}

//export goHIDOpened
func goHIDOpened(h C.uintptr_t, result C.int) {
	d := cgo.Handle(h).Value().(*darwinDevice)
	if result != 0 {
		d.started <- fmt.Errorf("IOHIDManagerOpen failed: 0x%08X", uint32(result))
		return
	}
	d.started <- nil
}

//export goHIDMatched
func goHIDMatched(h C.uintptr_t, dev C.uintptr_t, ble C.int) {
	d := cgo.Handle(h).Value().(*darwinDevice)
	d.mu.Lock()
	before := transportOf(d.current)
	d.pads = append(d.pads, padRef{ref: uintptr(dev), ble: ble != 0})
	d.current = d.pick()
	after := transportOf(d.current)
	d.mu.Unlock()
	if after != before {
		d.emit(Event{Kind: Connected, Transport: after})
	}
}

//export goHIDRemoved
func goHIDRemoved(h C.uintptr_t, dev C.uintptr_t) {
	d := cgo.Handle(h).Value().(*darwinDevice)
	d.mu.Lock()
	before := transportOf(d.current)
	for i, p := range d.pads {
		if p.ref == uintptr(dev) {
			C.hidRelease(C.uintptr_t(p.ref))
			d.pads = append(d.pads[:i], d.pads[i+1:]...)
			break
		}
	}
	d.current = d.pick()
	after := transportOf(d.current)
	d.mu.Unlock()
	if after == "" {
		d.emit(Event{Kind: Disconnected, Transport: before})
	} else if after != before {
		d.emit(Event{Kind: Connected, Transport: after})
	}
}

//export goHIDReport
func goHIDReport(h C.uintptr_t, dev C.uintptr_t, data *C.uint8_t, n C.long) {
	d := cgo.Handle(h).Value().(*darwinDevice)
	d.mu.Lock()
	cur := d.current
	d.mu.Unlock()
	if cur == nil || cur.ref != uintptr(dev) {
		return // the same pad on another transport; listen to one
	}
	d.emit(Event{Kind: Report, Transport: transportOf(cur), Data: C.GoBytes(unsafe.Pointer(data), C.int(n))})
}
