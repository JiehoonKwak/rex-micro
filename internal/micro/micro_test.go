package micro

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// fakePad answers device.status, but first lets "another app" reply to its own
// request that happens to carry the same id.
type fakePad struct {
	events chan hid.Event
}

func (f *fakePad) Events() <-chan hid.Event { return f.events }
func (f *fakePad) Transport() string        { return hid.TransportUSB }
func (f *fakePad) Close() error             { close(f.events); return nil }
func (f *fakePad) Send(frame func(string) [][]byte) error {
	var d protocol.Decoder
	for _, r := range frame(hid.TransportUSB) {
		for _, m := range d.Feed(r) {
			if m.Name() != "device.status" || m.ID == nil {
				continue
			}
			id := *m.ID
			var out []hid.Event
			out = append(out, reply(`{"result":{"ok":1},"id":`+itoa(id)+`,"method":"v.oai.thstatus"}`)...)
			out = append(out, reply(`{"result":{"version":"0.6.2","battery":99},"id":`+itoa(id)+`,"method":"device.status"}`)...)
			for _, ev := range out {
				f.events <- ev // buffered; the test sizes it
			}
		}
	}
	return nil
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func reply(msg string) []hid.Event {
	var out []hid.Event
	for _, r := range protocol.Frame([]byte(msg), "USB") {
		out = append(out, hid.Event{Kind: hid.Report, Transport: hid.TransportUSB, Data: r})
	}
	return out
}

// Review 2026-11-05: ChatGPT's numbered thstatus reply was taken as our
// device.status reply, so the status came back empty and the conflict went unseen.
func TestReplyMustMatchMethod(t *testing.T) {
	dev := &fakePad{events: make(chan hid.Event, 16)}
	p := New(dev)
	defer p.Close()
	foreign := make(chan string, 1)
	go func() {
		for ev := range p.Events() {
			if ev.Foreign != "" {
				foreign <- ev.Foreign
			}
		}
	}()
	st, err := p.Status(time.Second)
	if err != nil || st.Version != "0.6.2" {
		t.Fatalf("status = %+v, %v; want firmware 0.6.2", st, err)
	}
	select {
	case m := <-foreign:
		if m != "v.oai.thstatus" {
			t.Fatalf("foreign = %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("the other app's reply was not reported")
	}
}
