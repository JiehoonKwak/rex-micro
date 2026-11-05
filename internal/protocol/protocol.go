// Package protocol frames and parses the Codex Micro's vendor JSON-RPC channel.
//
// Facts here follow FreeMicro's docs/PROTOCOL.md (MIT, verified on firmware
// v0.4.1): messages are CRLF-terminated JSON split into reports of
// [0x02][len][data...]. USB takes 63-byte buffers; Bluetooth LE takes 64 bytes
// with the report ID repeated as byte 0. A misframed write still "succeeds" and
// is silently dropped, so only a device.status reply proves the path works.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

const (
	reportID    = 6
	opcodeData  = 0x02
	reportUSB   = 63
	reportBLE   = 64
	maxChunk    = reportUSB - 2
	transportBT = "Bluetooth Low Energy"
)

// Frame splits a JSON payload into output reports for the given transport.
func Frame(payload []byte, transport string) [][]byte { return FrameEOL(payload, transport, "\r\n") }

// FrameEOL is Frame with an explicit message terminator.
func FrameEOL(payload []byte, transport, eol string) [][]byte {
	ble := transport == transportBT
	size, prefix := reportUSB, 0
	if ble {
		size, prefix = reportBLE, 1
	}
	data := append(append([]byte{}, payload...), eol...)
	var out [][]byte
	for off := 0; off < len(data); {
		end := min(off+maxChunk, len(data))
		if len(data)-end == 1 {
			// Never leave the final "\n" alone in a report: firmware 0.6.2 reads
			// it as an empty message and answers "JSON error - EmptyInput".
			end--
		}
		r := make([]byte, size)
		if ble {
			r[0] = reportID
		}
		r[prefix] = opcodeData
		r[prefix+1] = byte(end - off)
		copy(r[prefix+2:], data[off:end])
		out = append(out, r)
		off = end
	}
	return out
}

// Decoder reassembles messages that span reports.
type Decoder struct{ buf []byte }

// Feed consumes one input report and returns the messages it completed.
// macOS may or may not include the report ID as byte 0; both layouts work.
func (d *Decoder) Feed(raw []byte) []Message {
	start := -1
	switch {
	case len(raw) >= 3 && raw[0] == reportID && raw[1] == opcodeData:
		start = 1
	case len(raw) >= 2 && raw[0] == opcodeData:
		start = 0
	}
	if start < 0 {
		return nil
	}
	n := int(raw[start+1])
	body := raw[start+2:]
	if n < len(body) {
		body = body[:n]
	}
	d.buf = append(d.buf, body...)
	var out []Message
	for {
		i := bytes.Index(d.buf, []byte("\r\n"))
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(d.buf[:i])
		d.buf = d.buf[i+2:]
		if len(line) == 0 {
			continue
		}
		var m Message
		if json.Unmarshal(line, &m) == nil {
			m.Raw = append([]byte{}, line...)
			out = append(out, m)
		}
	}
	if len(d.buf) > 64*1024 { // garbage without a terminator; start over
		d.buf = nil
	}
	return out
}

// Message is one JSON-RPC message in either compact ({m,p,id}) or standard form.
type Message struct {
	M      string          `json:"m,omitempty"`
	Method string          `json:"method,omitempty"`
	P      json.RawMessage `json:"p,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     *int            `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
	Raw    []byte          `json:"-"`
}

func (m Message) Name() string {
	if m.M != "" {
		return m.M
	}
	return m.Method
}

func (m Message) Args() json.RawMessage {
	if len(m.P) > 0 {
		return m.P
	}
	return m.Params
}

// Request builds a request that expects a reply.
func Request(method string, id int) []byte {
	b, _ := json.Marshal(map[string]any{"m": method, "id": id})
	return b
}

// Notify builds a fire-and-forget notification. v.oai.* methods reject an id.
func Notify(method string, params any) []byte {
	b, _ := json.Marshal(map[string]any{"m": method, "p": params})
	return b
}

// Input is a decoded physical input.
type Input struct {
	Kind  InputKind
	Key   string  // AG00..AG05, ACT06..ACT12, ENC_CLK
	Down  bool    // for keys
	Angle float64 // joystick, 0..1
	Dist  float64 // joystick, 0..1
}

type InputKind int

const (
	InputNone InputKind = iota
	InputKey
	InputDial // Key is ENC_CW or ENC_CC
	InputStick
)

// ParseInput turns a device notification into an Input.
func ParseInput(m Message) (Input, bool) {
	switch m.Name() {
	case "v.oai.hid":
		var p struct {
			K   string `json:"k"`
			Act int    `json:"act"`
		}
		if json.Unmarshal(m.Args(), &p) != nil || p.K == "" {
			return Input{}, false
		}
		if p.K == "ENC_CW" || p.K == "ENC_CC" {
			// Detent ticks carry arbitrary act values and have no release.
			// Firmware 0.6.2 names the turns the other way round: a clockwise
			// click arrives as ENC_CC (measured on hardware, 2026-11-05). Input
			// uses the physical direction so ENC_CW always means clockwise.
			key := "ENC_CW"
			if p.K == "ENC_CW" {
				key = "ENC_CC"
			}
			return Input{Kind: InputDial, Key: key}, true
		}
		return Input{Kind: InputKey, Key: p.K, Down: p.Act == 1}, true
	case "v.oai.rad":
		var p struct {
			A float64 `json:"a"`
			D float64 `json:"d"`
		}
		if json.Unmarshal(m.Args(), &p) != nil {
			return Input{}, false
		}
		return Input{Kind: InputStick, Angle: p.A, Dist: p.D}, true
	}
	return Input{}, false
}

// Effects understood by the lighting methods.
var Effects = map[string]int{
	"off": 0, "solid": 1, "snake": 2, "rainbow": 3, "breath": 4, "gradient": 5, "shallow_breath": 6,
}

// Light is one lighting side: effect, brightness, speed, color.
type Light struct {
	Effect     int
	Brightness float64
	Speed      float64
	Color      int // 0xRRGGBB
}

func (l Light) side() map[string]any {
	return map[string]any{"e": l.Effect, "b": round(l.Brightness), "s": round(l.Speed), "c": l.Color}
}

func round(f float64) float64 { return math.Round(f*1000) / 1000 }

// AgentKeys builds a v.oai.thstatus notification lighting Agent Keys 0-5.
func AgentKeys(lights [6]Light) []byte {
	entries := make([]map[string]any, 0, 6)
	for i, l := range lights {
		e := l.side()
		e["id"] = i
		entries = append(entries, e)
	}
	return Notify("v.oai.thstatus", entries)
}

// Ambient builds a v.oai.rgbcfg notification for the underglow and key backlight.
func Ambient(ambient, keys Light) []byte {
	return Notify("v.oai.rgbcfg", map[string]any{"ambient": ambient.side(), "keys": keys.side()})
}

// ParseColor accepts "#RRGGBB" or "RRGGBB".
func ParseColor(s string) (int, error) {
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	var c int
	if len(s) != 6 {
		return 0, fmt.Errorf("color %q is not #RRGGBB", s)
	}
	if _, err := fmt.Sscanf(s, "%06x", &c); err != nil {
		return 0, fmt.Errorf("color %q is not #RRGGBB", s)
	}
	return c, nil
}
