// Package hid opens the Codex Micro's vendor HID channel.
//
// The pad exposes one IOHIDDevice per transport (USB, Bluetooth LE). Every key,
// dial and joystick event, and every lighting command, travels over the
// vendor collection (usage page 0xFF00, report ID 6). This package only moves
// raw reports; framing lives in package protocol.
package hid

import "errors"

const (
	VendorID  = 0x303A
	ProductID = 0x8360
	ReportID  = 6

	TransportUSB = "USB"
	TransportBLE = "Bluetooth Low Energy"
)

// EventKind says what an Event carries.
type EventKind int

const (
	Connected EventKind = iota
	Report
	Disconnected
)

// Event is one thing that happened on the device.
type Event struct {
	Kind      EventKind
	Transport string
	Data      []byte
}

// Access is the Input Monitoring permission state for this process.
type Access int

const (
	AccessUnknown Access = iota
	AccessGranted
	AccessDenied
)

func (a Access) String() string {
	switch a {
	case AccessGranted:
		return "granted"
	case AccessDenied:
		return "denied"
	}
	return "not determined"
}

// ErrUnsupported is returned on platforms without an implementation.
var ErrUnsupported = errors.New("the Codex Micro vendor channel is only implemented on macOS")

// ErrNotConnected is returned by Write when no pad is open.
var ErrNotConnected = errors.New("Codex Micro is not connected")

// Device is an open, hot-pluggable connection to the pad.
type Device interface {
	// Events delivers Connected, Report and Disconnected events. It is closed by Close.
	Events() <-chan Event
	// Send writes one message to the open pad. frame turns the message into
	// output reports for that pad's transport; every report goes to the same
	// pad, and concurrent Sends never interleave.
	Send(frame func(transport string) [][]byte) error
	// Transport returns the transport of the open pad, or "" when none is open.
	Transport() string
	Close() error
}
