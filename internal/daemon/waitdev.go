package daemon

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/hid"
)

// waitingDevice keeps trying to open the pad, so the daemon stays up (and
// answers `rex-micro status`) while macOS has not granted Input Monitoring yet.
type waitingDevice struct {
	events chan hid.Event
	stop   chan struct{}
	mu     sync.Mutex
	dev    hid.Device
}

func openWaiting(logger *log.Logger) hid.Device {
	w := &waitingDevice{events: make(chan hid.Event, 256), stop: make(chan struct{})}
	go w.run(logger)
	return w
}

func (w *waitingDevice) run(logger *log.Logger) {
	defer close(w.events)
	warned := false
	for {
		var dev hid.Device
		err := errNoAccess
		if hid.CheckAccess() == hid.AccessGranted {
			dev, err = hid.Open()
		}
		if err == nil {
			w.mu.Lock()
			w.dev = dev
			w.mu.Unlock()
			if warned {
				logger.Printf("Input Monitoring granted; opening the pad")
			}
			for {
				select {
				case e, ok := <-dev.Events():
					if !ok {
						return
					}
					w.events <- e
				case <-w.stop:
					return
				}
			}
		}
		if !warned {
			logger.Printf("cannot open the pad yet (%v); allow rex-micro under System Settings > Privacy & Security > Input Monitoring", err)
			warned = true
		}
		select {
		case <-time.After(3 * time.Second):
		case <-w.stop:
			return
		}
	}
}

var errNoAccess = errors.New("Input Monitoring is not granted")

func (w *waitingDevice) current() hid.Device {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dev
}

func (w *waitingDevice) Events() <-chan hid.Event { return w.events }

func (w *waitingDevice) Send(frame func(string) [][]byte) error {
	if d := w.current(); d != nil {
		return d.Send(frame)
	}
	return hid.ErrNotConnected
}

func (w *waitingDevice) Transport() string {
	if d := w.current(); d != nil {
		return d.Transport()
	}
	return ""
}

func (w *waitingDevice) Close() error {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	if d := w.current(); d != nil {
		return d.Close()
	}
	return nil
}
