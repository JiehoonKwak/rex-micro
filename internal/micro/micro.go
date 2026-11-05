// Package micro talks JSON-RPC to a Codex Micro over a raw hid.Device.
package micro

import (
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

// Send frames and writes one message.
func (p *Pad) Send(payload []byte) error {
	return p.dev.Send(func(transport string) [][]byte { return protocol.Frame(payload, transport) })
}

// ErrTimeout means the pad accepted the write but never answered.
var ErrTimeout = errors.New("the pad did not answer (framing, permission or firmware problem)")

// Call sends a request and waits for its reply.
func (p *Pad) Call(method string, timeout time.Duration) (protocol.Message, error) {
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	w := make(chan protocol.Message, 1)
	p.waiters[id] = waiter{method: method, ch: w}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.waiters, id); p.mu.Unlock() }()
	if err := p.Send(protocol.Request(method, id)); err != nil {
		return protocol.Message{}, err
	}
	select {
	case m := <-w:
		return m, nil
	case <-time.After(timeout):
		return protocol.Message{}, ErrTimeout
	}
}

// Status performs the device.status round trip, the only proof the channel works.
func (p *Pad) Status(timeout time.Duration) (Status, error) {
	m, err := p.Call("device.status", timeout)
	if err != nil {
		return Status{}, err
	}
	var s Status
	if len(m.Error) > 0 {
		return s, errors.New("device.status error: " + string(m.Error))
	}
	err = json.Unmarshal(m.Result, &s)
	return s, err
}
