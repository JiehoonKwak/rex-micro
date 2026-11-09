// Package micro talks JSON-RPC to a Codex Micro over a raw hid.Device.
package micro

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// waiter is a pending request. Other host apps share the pad and number their
// requests too, so a reply must match the method as well as the id.
type waiter struct {
	method string
	ch     chan protocol.Message
}

// Event is what a Pad reports to its user.
type Event struct {
	Connected    bool   // pad appeared (Transport set)
	Disconnected bool   // pad went away
	Transport    string //
	Input        *protocol.Input
	// Foreign names a lighting method another host app sent to the pad.
	// rex-micro sends lighting as id-less notifications, so a lighting reply
	// carrying a numeric id answers someone else (e.g. ChatGPT).
	Foreign string
}

// Status is the reply to device.status.
type Status struct {
	Version      string  `json:"version"`
	ProfileIndex int     `json:"profile_index"`
	LayerIndex   int     `json:"layer_index"`
	Battery      float64 `json:"battery"`
	IsCharging   bool    `json:"is_charging"`
}

// Pad wraps a device with framing, request/reply matching and input decoding.
type Pad struct {
	dev     hid.Device
	events  chan Event
	dec     protocol.Decoder
	mu      sync.Mutex
	wmu     sync.Mutex // one message, with its recovery, at a time
	nextID  int
	waiters map[int]waiter
	// Raw, when set, receives every decoded message (for `rex-micro monitor`).
	Raw func(protocol.Message)
	// RawReport, when set, receives every input report as delivered by the OS.
	RawReport func([]byte)
}

func New(dev hid.Device) *Pad {
	p := &Pad{dev: dev, events: make(chan Event, 256), nextID: 100, waiters: map[int]waiter{}}
	go p.loop()
	return p
}

func (p *Pad) Events() <-chan Event { return p.events }
func (p *Pad) Transport() string    { return p.dev.Transport() }
func (p *Pad) Close() error         { return p.dev.Close() }

func (p *Pad) loop() {
	defer close(p.events)
	for e := range p.dev.Events() {
		switch e.Kind {
		case hid.Connected:
			p.dec = protocol.Decoder{}
			p.events <- Event{Connected: true, Transport: e.Transport}
		case hid.Disconnected:
			p.events <- Event{Disconnected: true, Transport: e.Transport}
		case hid.Report:
			if p.RawReport != nil {
				p.RawReport(e.Data)
			}
			for _, m := range p.dec.Feed(e.Data) {
				if p.Raw != nil {
					p.Raw(m)
				}
				if m.ID != nil {
					p.mu.Lock()
					w, ok := p.waiters[*m.ID]
					ok = ok && (m.Name() == "" || m.Name() == w.method)
					if ok {
						delete(p.waiters, *m.ID)
					}
					p.mu.Unlock()
					if ok {
						w.ch <- m
						continue
					}
					if len(m.Result) > 0 && strings.HasPrefix(m.Name(), "v.oai.") {
						p.events <- Event{Foreign: m.Name()}
						continue
					}
				}
				if in, ok := protocol.ParseInput(m); ok {
					p.events <- Event{Input: &in}
				}
			}
		}
	}
}

// sendTries bounds the attempts at one message. A write can fail partway
// (IOHIDDeviceSetReport returns an error, more often while another app writes to
// the pad), leaving the first reports of the message in the pad's buffer.
const sendTries = 4

func (p *Pad) write(payload []byte) error {
	return p.dev.Send(func(transport string) [][]byte { return protocol.Frame(payload, transport) })
}

// deliver writes one message, and when the write fails partway ends it before
// any other message of this process can follow it: the pad would otherwise
// join the two and discard both. A bare CRLF ends it; the pad answers that
// with an id-less JSON error.
func (p *Pad) deliver(payload []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	err := p.write(payload)
	if err != nil {
		_ = p.write(nil)
		time.Sleep(30 * time.Millisecond)
	}
	return err
}

// Send frames and writes one notification, retrying a write that failed partway.
func (p *Pad) Send(payload []byte) error {
	var err error
	for try := 0; try < sendTries; try++ {
		if err = p.deliver(payload); err == nil {
			return nil
		}
	}
	return err
}

// ErrTimeout means the pad accepted the write but never answered.
var ErrTimeout = errors.New("the pad did not answer (framing, permission or firmware problem)")

// newID numbers a request. Work Louder's own client keeps ids under 999, so
// they wrap rather than grow without end.
func (p *Pad) newID() int {
	p.nextID++
	if p.nextID >= 999 {
		p.nextID = 100
	}
	return p.nextID
}

// Call sends a request (params may be nil) and waits for its reply. A write
// that fails partway is retried under a new id: the cut-off copy may still
// draw an error reply carrying the old one.
func (p *Pad) Call(method string, params any, timeout time.Duration) (protocol.Message, error) {
	var err error
	for try := 0; try < sendTries; try++ {
		p.mu.Lock()
		id := p.newID()
		w := make(chan protocol.Message, 1)
		p.waiters[id] = waiter{method: method, ch: w}
		p.mu.Unlock()
		forget := func() { p.mu.Lock(); delete(p.waiters, id); p.mu.Unlock() }
		if err = p.deliver(protocol.Request(method, params, id)); err != nil {
			forget()
			continue
		}
		select {
		case m := <-w:
			forget()
			return m, nil
		case <-time.After(timeout):
			forget()
			return protocol.Message{}, ErrTimeout
		}
	}
	return protocol.Message{}, err
}

// call is Call that turns an error reply into an error and decodes the result.
func (p *Pad) call(method string, params any, timeout time.Duration, out any) error {
	m, err := p.Call(method, params, timeout)
	if err != nil {
		return err
	}
	if len(m.Error) > 0 {
		return errors.New(method + " error: " + string(m.Error))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(m.Result, out)
}

// Status performs the device.status round trip, the only proof the channel works.
func (p *Pad) Status(timeout time.Duration) (Status, error) {
	var s Status
	err := p.call("device.status", nil, timeout, &s)
	return s, err
}

// FocusApp tells the pad which app is in front, by bundle ID. The pad switches
// to the layer its keymap links to that app and ignores an app it has no link
// for.
func (p *Pad) FocusApp(process string) error {
	return p.call("host.focused_app", map[string]string{"appName": process, "process": process}, 2*time.Second, nil)
}

// ReadFile reads a text file from the pad's filesystem.
func (p *Pad) ReadFile(name string) (string, error) {
	var r struct {
		Data *string `json:"data"`
	}
	if err := p.call("fs.read", map[string]string{"file": name}, 5*time.Second, &r); err != nil {
		return "", err
	}
	if r.Data == nil {
		return "", errors.New("fs.read " + name + ": no data in the reply")
	}
	return *r.Data, nil
}

// fileChunk is the raw bytes per fs.writebin call; base64 makes it the 4096
// characters Work Louder Input sends.
const fileChunk = 3072

// WriteFile replaces a file on the pad and reads it back to check it. The pad
// collects the chunks in a temporary file and renames it over name on the
// last one, so a write that stops early leaves name as it was. Any failure
// starts over from the first chunk: resending only the failed one could, after
// a lost reply to the last chunk, rename a fragment over name.
func (p *Pad) WriteFile(name, data string) error {
	var err error
	for try := 0; try < 3; try++ {
		if err = p.writeOnce(name, []byte(data)); err != nil {
			continue // a cut write was already ended by deliver
		}
		var back string
		if back, err = p.ReadFile(name); err == nil && back != data {
			err = errors.New("the pad's " + name + " differs from what was written")
		}
		if err == nil {
			return nil
		}
	}
	return err
}

func (p *Pad) writeOnce(name string, raw []byte) error {
	for off := 0; off == 0 || off < len(raw); off += fileChunk {
		end := min(off+fileChunk, len(raw))
		params := map[string]any{
			"file": name, "data": base64.StdEncoding.EncodeToString(raw[off:end]),
			"append": true, "completed": end == len(raw), "offset": off,
		}
		if err := p.call("fs.writebin", params, 5*time.Second, nil); err != nil {
			return err
		}
	}
	return nil
}
