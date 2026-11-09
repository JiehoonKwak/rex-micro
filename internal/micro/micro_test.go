package micro

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
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

// filePad keeps files the way firmware 0.6.2 does (fs.writebin collects a
// temporary file and renames it on completed) and cuts the writes listed in
// fail short: half the reports arrive, then SetReport fails. Like the pad, it
// joins whatever arrives until CRLF, so a cut-off message spoils the next one
// unless something ends it first.
type filePad struct {
	events chan hid.Event
	mu     sync.Mutex
	sends  int
	fail   map[int]bool
	dec    protocol.Decoder
	files  map[string]string
	tmp    []byte
	// cut, when set, is closed after the first cut write, which then takes a
	// moment to return, as a real failing SetReport can.
	cut chan struct{}
}

func (f *filePad) Events() <-chan hid.Event { return f.events }
func (f *filePad) Transport() string        { return hid.TransportUSB }
func (f *filePad) Close() error             { close(f.events); return nil }
func (f *filePad) Send(frame func(string) [][]byte) error {
	f.mu.Lock()
	f.sends++
	failed := f.fail[f.sends]
	reports := frame(hid.TransportUSB)
	if failed {
		reports = reports[:len(reports)/2]
	}
	for _, r := range reports {
		for _, m := range f.dec.Feed(r) {
			f.handle(m)
		}
	}
	cut := f.cut
	if failed {
		f.cut = nil
	}
	f.mu.Unlock()
	if !failed {
		return nil
	}
	if cut != nil {
		close(cut)
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("IOHIDDeviceSetReport failed: 0xE00002BC")
}

func (f *filePad) handle(m protocol.Message) {
	if m.ID == nil {
		return
	}
	var p struct {
		File      string `json:"file"`
		Data      string `json:"data"`
		Offset    int    `json:"offset"`
		Completed bool   `json:"completed"`
	}
	_ = json.Unmarshal(m.Args(), &p)
	result := `null`
	switch m.Name() {
	case "fs.writebin":
		chunk, _ := base64.StdEncoding.DecodeString(p.Data)
		if p.Offset == 0 {
			f.tmp = nil
		}
		f.tmp = append(f.tmp, chunk...)
		if p.Completed {
			f.files[p.File] = string(f.tmp)
		}
		result = `{"bytes_written":` + itoa(len(chunk)) + `}`
	case "fs.read":
		b, _ := json.Marshal(map[string]string{"data": f.files[p.File]})
		result = string(b)
	default:
		return
	}
	for _, ev := range reply(`{"result":` + result + `,"id":` + itoa(*m.ID) + `,"method":"` + m.Name() + `"}`) {
		f.events <- ev
	}
}

// A write cut short is ended and retried, and the file still arrives whole:
// across chunks (the keymap needs two) and with the pad's own temp-and-rename.
func TestWriteFileSurvivesCutWrites(t *testing.T) {
	data := strings.Repeat("0123456789abcdef", 300) // 4800 bytes: two chunks
	dev := &filePad{events: make(chan hid.Event, 4096), fail: map[int]bool{1: true, 3: true}, files: map[string]string{"keymap.json": "old"}}
	p := New(dev)
	defer p.Close()
	go func() {
		for range p.Events() {
		}
	}()
	start := time.Now()
	if err := p.WriteFile("keymap.json", data); err != nil {
		t.Fatal(err)
	}
	if dev.files["keymap.json"] != data {
		t.Fatalf("pad has %d bytes, want %d", len(dev.files["keymap.json"]), len(data))
	}
	// Retrying without ending the cut-off message waits out a reply timeout.
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("write took %v; a cut write was not ended before the retry", d)
	}
}

// A request sent while another message of this process is being cut short
// must not slip in before that message is ended, or the pad joins the two and
// drops the request (review of PR 9).
func TestRequestWaitsForACutMessageToEnd(t *testing.T) {
	cut := make(chan struct{})
	dev := &filePad{events: make(chan hid.Event, 4096), fail: map[int]bool{1: true}, files: map[string]string{"keymap.json": "km"}, cut: cut}
	p := New(dev)
	defer p.Close()
	go func() {
		for range p.Events() {
		}
	}()
	go func() { _ = p.Send(protocol.AgentKeys([6]protocol.Light{})) }()
	<-cut
	start := time.Now()
	got, err := p.ReadFile("keymap.json")
	if err != nil || got != "km" {
		t.Fatalf("read = %q, %v after %v", got, err, time.Since(start))
	}
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
