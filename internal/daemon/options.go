package daemon

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/macos"
	"github.com/JiehoonKwak/rex-micro/internal/rex"
)

// Options are the daemon's dependencies; tests replace the device and Rex.
type Options struct {
	ConfigPath string
	Logger     *log.Logger
	Device     hid.Device // nil opens the real pad
	Runner     rex.Runner // nil runs the rex CLI
	Stop       <-chan struct{}
	// Update reports true after installing a newer executable; the daemon
	// then exits so launchd restarts it. Nil disables the check.
	Update func() (bool, error)
	// FrontApp names the app in front; nil asks macOS.
	FrontApp func() string
	// Shortcut sends a key combination; nil posts it through macOS. Trusted
	// reports Accessibility for it; nil asks macOS.
	Shortcut func(macos.Shortcut) error
	// HoldShortcut presses or releases a held shortcut; nil asks macOS.
	HoldShortcut func(macos.Shortcut, bool) error
	Trusted      func() bool
	// FreshTrusted asks a new process whether Accessibility is granted.
	// macOS does not tell a running process about a grant, so the daemon
	// restarts when a fresh check succeeds. Nil runs `Self _trusted`.
	FreshTrusted func() bool
	// Self is the rex-micro executable, which the daemon runs as
	// `Self _overlay` (the keys overlay) and `Self _trusted`. Only Run sets
	// it. Empty starts neither: under `go test` the executable is the test
	// binary, which would run the tests again and again (2026-11-09).
	Self string
}

// Run starts the daemon and blocks until SIGINT/SIGTERM.
func Run(cfgPath string, logger *log.Logger, update func() (bool, error)) error {
	if hid.CheckAccess() != hid.AccessGranted {
		logger.Printf("asking macOS for Input Monitoring; allow rex-micro in System Settings > Privacy & Security > Input Monitoring")
		hid.RequestAccess()
	}
	dev := openWaiting(logger)
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; close(stop) }()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return Start(Options{ConfigPath: cfgPath, Logger: logger, Device: dev, Runner: rex.ExecRunner{Path: RexPath()}, Stop: stop, Update: update, Self: self})
}
