package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameUSBAndBLE(t *testing.T) {
	usb := Frame([]byte(`{"m":"device.status","id":1}`), "USB")
	if len(usb) != 1 || len(usb[0]) != 63 || usb[0][0] != 0x02 || int(usb[0][1]) != 30 {
		t.Fatalf("usb frame = % x", usb[0][:4])
	}
	if !bytes.HasPrefix(usb[0][2:], []byte("{\"m\":\"device.status\",\"id\":1}\r\n")) {
		t.Fatalf("usb body = %q", usb[0][2:34])
	}
	ble := Frame([]byte(`{}`), "Bluetooth Low Energy")
	if len(ble[0]) != 64 || ble[0][0] != 6 || ble[0][1] != 0x02 || ble[0][2] != 4 {
		t.Fatalf("ble frame = % x", ble[0][:5])
	}
}

func TestLongMessageRoundTrips(t *testing.T) {
	payload := Notify("v.oai.thstatus", strings.Repeat("x", 150))
	reports := Frame(payload, "USB")
	if len(reports) != 3 {
		t.Fatalf("reports = %d", len(reports))
	}
	var d Decoder
	var got []Message
	for _, r := range reports {
		// macOS hands input callbacks the report ID first; the decoder takes both layouts.
		got = append(got, d.Feed(append([]byte{6}, r...))...)
	}
	if len(got) != 1 || got[0].Name() != "v.oai.thstatus" {
		t.Fatalf("decoded %+v", got)
	}
}

func TestParseInputs(t *testing.T) {
	var d Decoder
	feed := func(s string) Input {
		t.Helper()
		ms := d.Feed(Frame([]byte(s), "USB")[0])
		if len(ms) != 1 {
			t.Fatalf("decode %s: %d messages", s, len(ms))
		}
		in, ok := ParseInput(ms[0])
		if !ok {
			t.Fatalf("parse %s failed", s)
		}
		return in
	}
	if in := feed(`{"m":"v.oai.hid","p":{"k":"ACT07","act":1,"ag":0}}`); in.Kind != InputKey || in.Key != "ACT07" || !in.Down {
		t.Errorf("key down = %+v", in)
	}
	if in := feed(`{"m":"v.oai.hid","p":{"k":"AG03","act":0}}`); in.Down {
		t.Errorf("key up = %+v", in)
	}
	// Dial ticks carry arbitrary act values and must never be dropped.
	if in := feed(`{"m":"v.oai.hid","p":{"k":"ENC_CW","act":2}}`); in.Kind != InputDial {
		t.Errorf("dial = %+v", in)
	}
	if in := feed(`{"method":"v.oai.rad","params":{"a":0.5,"d":0.9}}`); in.Kind != InputStick || in.Angle != 0.5 {
		t.Errorf("stick = %+v", in)
	}
}

func TestAgentKeysMessage(t *testing.T) {
	var l [6]Light
	l[2] = Light{Effect: 4, Brightness: 0.8, Speed: 0.5, Color: 0xF59E0B}
	got := string(AgentKeys(l))
	if !strings.Contains(got, `{"b":0.8,"c":16096779,"e":4,"id":2,"s":0.5}`) || !strings.HasPrefix(got, `{"m":"v.oai.thstatus","p":[`) {
		t.Fatalf("thstatus = %s", got)
	}
}

func TestTerminatorNeverSplitAlone(t *testing.T) {
	for n := 1; n < 200; n++ {
		reports := Frame([]byte(strings.Repeat("x", n)), "USB")
		last := reports[len(reports)-1]
		if body := last[2 : 2+int(last[1])]; string(body) == "\n" {
			t.Fatalf("payload %d: last report holds only LF", n)
		}
		var d Decoder
		var got []Message
		for _, r := range reports {
			got = append(got, d.Feed(r)...)
		}
		_ = got // "x..." is not JSON; reassembly is covered elsewhere
	}
}

// Recorded on firmware 0.6.2: one clockwise click, then one counter-clockwise.
func TestDialUsesPhysicalDirection(t *testing.T) {
	for raw, want := range map[string]string{
		`{"m":"v.oai.hid","p":{"k":"ENC_CC","act":2}}`: "ENC_CW",
		`{"m":"v.oai.hid","p":{"k":"ENC_CW","act":2}}`: "ENC_CC",
	} {
		var m Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		if in, ok := ParseInput(m); !ok || in.Kind != InputDial || in.Key != want {
			t.Errorf("%s -> %+v, want %s", raw, in, want)
		}
	}
}
