package cli

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/JiehoonKwak/rex-micro/internal/agents"
	"github.com/JiehoonKwak/rex-micro/internal/config"
	"github.com/JiehoonKwak/rex-micro/internal/daemon"
	"github.com/JiehoonKwak/rex-micro/internal/engine"
	"github.com/JiehoonKwak/rex-micro/internal/hid"
	"github.com/JiehoonKwak/rex-micro/internal/micro"
	"github.com/JiehoonKwak/rex-micro/internal/protocol"
)

// step is one thing the user does on the pad.
type step struct {
	Ask    string   `json:"ask"`
	Expect []string `json:"expect"` // key ids that satisfy it; "AG*" any Agent Key; "STICK_<DIR>"
	Result string   `json:"result"` // pass, fail, skip, device (mapped in the pad's onboard keymap)
	Got    []string `json:"got"`
	Typed  string   `json:"typed,omitempty"` // keystrokes the pad typed into the terminal (should be empty)
	Angle  float64  `json:"angle,omitempty"`
}

type testReport struct {
	When      time.Time       `json:"when"`
	Version   string          `json:"version"`
	Transport string          `json:"transport"`
	Firmware  string          `json:"firmware"`
	Steps     []*step         `json:"steps"`
	Lights    map[string]bool `json:"lights"`
	AgentIDs  []string        `json:"agent_key_order"` // AG ids in physical order
	Messages  []string        `json:"messages"`
	Reports   []string        `json:"reports_hex"`
	Passed    bool            `json:"passed"`
}

func hardwareTest(args []string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("test is interactive; run it in a terminal")
	}
	if hid.CheckAccess() != hid.AccessGranted {
		hid.RequestAccess()
		return errors.New("this terminal needs Input Monitoring to read the pad; allow it in System Settings, then rerun")
	}
	release, err := daemon.Pause()
	if err != nil {
		return err
	}
	defer release()

	dev, err := hid.Open()
	if err != nil {
		return err
	}
	pad := micro.New(dev)
	defer pad.Close()
	rep := &testReport{When: time.Now(), Version: Version, Lights: map[string]bool{}}
	pad.Raw = func(m protocol.Message) { rep.Messages = append(rep.Messages, string(m.Raw)) }
	pad.RawReport = func(b []byte) { rep.Reports = append(rep.Reports, hex.EncodeToString(b)) }

	inputs := make(chan protocol.Input, 64)
	connected := make(chan string, 1)
	go func() {
		for ev := range pad.Events() {
			switch {
			case ev.Connected:
				connected <- ev.Transport
			case ev.Input != nil:
				inputs <- *ev.Input
			}
		}
	}()
	select {
	case rep.Transport = <-connected:
	case <-time.After(2 * time.Second):
		return errors.New("no Codex Micro found; plug it in over USB")
	}
	st, err := pad.Status(2 * time.Second)
	if err != nil {
		return fmt.Errorf("pad found but did not answer: %w", err)
	}
	rep.Firmware = st.Version
	fmt.Printf("Codex Micro over %s, firmware %s. The daemon is paused while this runs.\n\n", rep.Transport, st.Version)

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	restore := func() { _ = term.Restore(int(os.Stdin.Fd()), old) }
	defer restore()
	typed := make(chan byte, 64)
	go func() {
		b := make([]byte, 1)
		for {
			if n, err := os.Stdin.Read(b); err != nil || n == 0 {
				return
			}
			typed <- b[0]
		}
	}()
	say := func(format string, a ...any) { fmt.Print(strings.ReplaceAll(fmt.Sprintf(format, a...), "\n", "\r\n")) }

	cfg, _ := config.Parse("")
	steps := []*step{
		{Ask: "Agent Key, top row, left (next to the dial)", Expect: []string{"AG*"}},
		{Ask: "Agent Key, top row, right (next to the stick)", Expect: []string{"AG*"}},
		{Ask: "Agent Key, second row, 1st from left", Expect: []string{"AG*"}},
		{Ask: "Agent Key, second row, 2nd from left", Expect: []string{"AG*"}},
		{Ask: "Agent Key, second row, 3rd from left", Expect: []string{"AG*"}},
		{Ask: "Agent Key, second row, 4th from left", Expect: []string{"AG*"}},
		{Ask: "third row, 1st key (factory cap: FAST)", Expect: []string{"ACT06"}},
		{Ask: "third row, 2nd key (APPROVE)", Expect: []string{"ACT07"}},
		{Ask: "third row, 3rd key (REJECT)", Expect: []string{"ACT08"}},
		{Ask: "third row, 4th key (SPLIT)", Expect: []string{"ACT09"}},
		{Ask: "the wide key (MIC)", Expect: []string{"ACT10", "ACT11"}},
		{Ask: "bottom-right key (CODEX)", Expect: []string{"ACT12"}},
		{Ask: "press the dial", Expect: []string{"ENC_CLK"}},
		{Ask: "turn the dial one click clockwise", Expect: []string{"ENC_CW"}},
		{Ask: "turn the dial one click counter-clockwise", Expect: []string{"ENC_CC"}},
		{Ask: "push the stick UP, then let go", Expect: []string{"STICK_UP"}},
		{Ask: "push the stick RIGHT, then let go", Expect: []string{"STICK_RIGHT"}},
		{Ask: "push the stick DOWN, then let go", Expect: []string{"STICK_DOWN"}},
		{Ask: "push the stick LEFT, then let go", Expect: []string{"STICK_LEFT"}},
	}
	if len(args) > 0 && args[0] == "--lights" {
		// Lights only. Agent Key IDs follow the physical order on firmware 0.6.2.
		steps = nil
		rep.AgentIDs = []string{"AG00", "AG01", "AG02", "AG03", "AG04", "AG05"}
	} else {
		say("Part 1 of 2: inputs. Do what each line asks. s = skip, q = quit.\n\n")
	}
	rep.Steps = steps
	seenAG := map[string]bool{}
	quit := false
	for i, s := range steps {
		if quit {
			s.Result = "skip"
			continue
		}
		say("[%2d/%d] %s ... ", i+1, len(steps), s.Ask)
		s.Result = runStep(s, inputs, typed, cfg, seenAG)
		if s.Result == "quit" {
			s.Result, quit = "skip", true
		}
		detail := strings.Join(s.Got, " ")
		switch {
		case s.Result == "device":
			detail = fmt.Sprintf("mapped in the pad's own keymap: it typed %q, so rex-micro cannot see this key", s.Typed)
		case s.Typed != "":
			detail += fmt.Sprintf("  (the pad also typed %q into the terminal)", s.Typed)
		}
		say("%s  %s\n", strings.ToUpper(s.Result), detail)
		if strings.HasPrefix(s.Expect[0], "AG") && len(s.Got) > 0 {
			rep.AgentIDs = append(rep.AgentIDs, s.Got[0])
		}
	}

	say("\nPart 2 of 2: lights. Answer y or n.\n\n")
	order := rep.AgentIDs
	if len(order) != agents.Keys {
		order = []string{"AG00", "AG01", "AG02", "AG03", "AG04", "AG05"}
		say("(physical Agent Key order unknown; assuming AG00-AG05 left to right, top row first)\n")
	}
	states := []string{"working", "blocked", "error", "done", "idle", "empty"}
	names := []string{"blue, breathing", "amber, breathing", "red, breathing", "green", "dim white", "off"}
	var slots [agents.Keys]agents.Slot
	for pos, id := range order {
		idx := int(id[3] - '0')
		slots[idx] = agents.Slot{State: states[pos]}
	}
	_ = pad.Send(protocol.AgentKeys(agents.Lights(slots, cfg.Lighting)))
	purple := protocol.Light{Effect: 1, Brightness: 0.6, Color: 0x8B5CF6}
	_ = pad.Send(protocol.Ambient(purple, purple))
	say("Agent Keys, top row left to right then second row left to right, should be:\n  %s\n", strings.Join(names, " | "))
	rep.Lights["agent_keys_states"] = yes(typed, say, "Do they match? [y/n] ")
	rep.Lights["backlight_purple"] = yes(typed, say, "Is the key backlight or underglow purple? [y/n] ")
	var dark [agents.Keys]protocol.Light
	_ = pad.Send(protocol.AgentKeys(dark))
	_ = pad.Send(protocol.Ambient(protocol.Light{}, protocol.Light{}))
	rep.Lights["lights_off"] = yes(typed, say, "Are all lights now off? [y/n] ")

	rep.Passed = true
	var onboard []string
	for _, s := range steps {
		if s.Result == "device" {
			onboard = append(onboard, s.Ask)
			continue
		}
		rep.Passed = rep.Passed && s.Result == "pass" && s.Typed == ""
	}
	for _, v := range rep.Lights {
		rep.Passed = rep.Passed && v
	}
	restore()
	path := testReportPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	out, _ := json.MarshalIndent(rep, "", "  ")
	_ = os.WriteFile(path, out, 0o644)
	fmt.Println()
	if hint := stickHint(steps); hint != "" {
		fmt.Println(hint)
	}
	if len(onboard) > 0 {
		fmt.Printf("%d key(s) are mapped in the pad's onboard keymap and keep doing that without rex-micro:\n", len(onboard))
		for _, k := range onboard {
			fmt.Println("  -", k)
		}
		fmt.Println("To bind them with rex-micro instead, reset them to their default once in Work Louder Input.")
	}
	fmt.Println("results saved to", path)
	if !rep.Passed {
		return errors.New("hardware test did not pass; see the lines above")
	}
	fmt.Println("hardware test passed")
	return nil
}

func testReportPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "rex-micro", "test-results.json")
}

func runStep(s *step, inputs <-chan protocol.Input, typed <-chan byte, cfg *config.Config, seenAG map[string]bool) string {
	// Drain anything left over from the previous step.
	for len(inputs) > 0 {
		<-inputs
	}
	timeout := time.After(30 * time.Second)
	want := map[string]bool{}
	for _, e := range s.Expect {
		want[e] = true
	}
	got := map[string]bool{}
	released := false
	var peak protocol.Input
	var settle <-chan time.Time // runs after the pad typed something
	for {
		select {
		case <-settle:
			// The pad typed keystrokes and sent nothing on its own channel: the
			// key is mapped in the pad's onboard keymap (set by Work Louder Input).
			if len(s.Got) == 0 {
				return "device"
			}
		case b := <-typed:
			switch {
			case b == 's' && s.Typed == "":
				return "skip"
			case (b == 'q' && s.Typed == "") || b == 3:
				return "quit"
			default:
				s.Typed += string(rune(b))
				if settle == nil {
					settle = time.After(1500 * time.Millisecond)
				}
			}
		case <-timeout:
			return "fail"
		case in := <-inputs:
			switch in.Kind {
			case protocol.InputKey:
				if in.Down {
					if !got[in.Key] {
						s.Got = append(s.Got, in.Key)
					}
					got[in.Key] = true
				} else if got[in.Key] {
					released = true
				}
			case protocol.InputDial:
				if strings.HasPrefix(s.Expect[0], "STICK_") {
					continue // a late detent from the dial steps just before
				}
				s.Got = append(s.Got, in.Key)
				return verdict(want[in.Key])
			case protocol.InputStick:
				if in.Dist > peak.Dist {
					peak = in
				}
				if in.Dist == 0 && peak.Dist >= cfg.Stick.Threshold {
					dir := "STICK_" + engine.Direction(peak.Angle, cfg.Stick.AngleOffset)
					s.Got = append(s.Got, fmt.Sprintf("%s (a=%.2f)", dir, peak.Angle))
					s.Angle = peak.Angle
					return verdict(want[dir])
				}
				continue
			}
			if !released {
				continue
			}
			// A key went down and up: judge it, after a beat so the wide key's second switch lands.
			time.Sleep(80 * time.Millisecond)
		drain:
			for {
				select {
				case in := <-inputs:
					if in.Kind == protocol.InputKey && in.Down && !got[in.Key] {
						s.Got = append(s.Got, in.Key)
						got[in.Key] = true
					}
				default:
					break drain
				}
			}
			if want["AG*"] {
				id := s.Got[0]
				ok := len(s.Got) == 1 && strings.HasPrefix(id, "AG") && !seenAG[id]
				seenAG[id] = true
				return verdict(ok)
			}
			ok := len(got) == len(want)
			for k := range want {
				ok = ok && got[k]
			}
			return verdict(ok)
		}
	}
}

func verdict(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func yes(typed <-chan byte, say func(string, ...any), q string) bool {
	say(q)
	for b := range typed {
		switch b {
		case 'y', 'Y':
			say("y\n")
			return true
		case 'n', 'N', 3:
			say("n\n")
			return false
		}
	}
	return false
}

// stickHint suggests an angle_offset when the stick reads rotated.
// stickCenters is the angle the firmware reports for each direction:
// 0 points right and angles grow clockwise (measured on firmware 0.6.2).
var stickCenters = map[string]float64{"STICK_UP": 0.75, "STICK_RIGHT": 0, "STICK_DOWN": 0.25, "STICK_LEFT": 0.5}

func stickHint(steps []*step) string {
	want := stickCenters
	var sx, sy float64
	n := 0
	for _, s := range steps {
		w, ok := want[s.Expect[0]]
		if !ok || len(s.Got) == 0 || !strings.HasPrefix(s.Got[0], "STICK_") {
			continue
		}
		d := (w - s.Angle) * 2 * math.Pi
		sx, sy = sx+math.Cos(d), sy+math.Sin(d)
		n++
	}
	if n < 2 {
		return ""
	}
	off := math.Atan2(sy, sx) / (2 * math.Pi)
	if off < 0 {
		off++
	}
	if math.Abs(off) < 0.05 || math.Abs(off-1) < 0.05 {
		return ""
	}
	return fmt.Sprintf("stick reads rotated: set [stick] angle_offset = %.2f in your config", off)
}

// monitor prints raw pad events.
func monitor() error {
	release, err := daemon.Pause()
	if err != nil {
		return err
	}
	defer release()
	dev, err := hid.Open()
	if err != nil {
		return err
	}
	pad := micro.New(dev)
	defer pad.Close()
	pad.Raw = func(m protocol.Message) { fmt.Printf("%s  %s\n", time.Now().Format("15:04:05.000"), m.Raw) }
	fmt.Println("listening; press keys on the pad (Ctrl-C to stop)")
	for ev := range pad.Events() {
		switch {
		case ev.Connected:
			fmt.Println("connected over", ev.Transport)
		case ev.Disconnected:
			fmt.Println("disconnected")
		}
	}
	return nil
}

// lights drives the Agent Keys without the daemon.
func lights(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rex-micro lights demo|off")
	}
	release, err := daemon.Pause()
	if err != nil {
		return err
	}
	defer release()
	dev, err := hid.Open()
	if err != nil {
		return err
	}
	pad := micro.New(dev)
	defer pad.Close()
	select {
	case <-waitConnected(pad):
	case <-time.After(2 * time.Second):
		return errors.New("no Codex Micro found")
	}
	cfg, _ := config.Load(config.Path())
	var dark [agents.Keys]protocol.Light
	switch args[0] {
	case "off":
		_ = pad.Send(protocol.AgentKeys(dark))
		return pad.Send(protocol.Ambient(protocol.Light{}, protocol.Light{}))
	case "probe":
		return lightProbe(pad)
	case "demo":
		// A scripted story for screen recordings: agents start, one asks for
		// approval, one finishes, one fails.
		frames := [][agents.Keys]string{
			{"idle", "idle", "idle", "empty", "empty", "empty"},
			{"working", "idle", "idle", "empty", "empty", "empty"},
			{"working", "working", "idle", "empty", "empty", "empty"},
			{"working", "working", "working", "working", "empty", "empty"},
			{"blocked", "working", "working", "working", "empty", "empty"},
			{"blocked", "done", "working", "working", "empty", "empty"},
			{"working", "done", "working", "error", "empty", "empty"},
			{"done", "done", "done", "error", "empty", "empty"},
		}
		layer := protocol.Light{Effect: 1, Brightness: 0.5, Color: 0x8B5CF6}
		_ = pad.Send(protocol.Ambient(layer, layer))
		for _, f := range frames {
			var slots [agents.Keys]agents.Slot
			for i, st := range f {
				slots[i] = agents.Slot{State: st}
			}
			fmt.Println(strings.Join(f[:], " "))
			if err := pad.Send(protocol.AgentKeys(agents.Lights(slots, cfg.Lighting))); err != nil {
				return err
			}
			time.Sleep(1800 * time.Millisecond)
		}
		return nil
	}
	return fmt.Errorf("unknown lights command %q", args[0])
}

func waitConnected(pad *micro.Pad) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		once := false
		for ev := range pad.Events() { // keep draining so replies still flow
			if ev.Connected && !once {
				once = true
				close(ch)
			}
		}
	}()
	return ch
}

// lightProbe asks the user about simple lighting patterns, one at a time, to
// find what the firmware does with v.oai.thstatus. Answers go to the report file.
func lightProbe(pad *micro.Pad) error {
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), old)
	typed := make(chan byte, 16)
	go func() {
		b := make([]byte, 1)
		for {
			if n, err := os.Stdin.Read(b); err != nil || n == 0 {
				return
			}
			typed <- b[0]
		}
	}()
	say := func(format string, a ...any) { fmt.Print(strings.ReplaceAll(fmt.Sprintf(format, a...), "\n", "\r\n")) }
	all := func(l protocol.Light) [agents.Keys]protocol.Light {
		var out [agents.Keys]protocol.Light
		for i := range out {
			out[i] = l
		}
		return out
	}
	red := protocol.Light{Effect: 1, Brightness: 1, Color: 0xFF0000}
	var one [agents.Keys]protocol.Light
	one[0] = protocol.Light{Effect: 1, Brightness: 1, Color: 0x00FF00}
	rainbow := [agents.Keys]protocol.Light{}
	for i, c := range []int{0xFF0000, 0xFF8800, 0xFFFF00, 0x00FF00, 0x0066FF, 0xAA00FF} {
		rainbow[i] = protocol.Light{Effect: 1, Brightness: 1, Color: c}
	}
	probes := []struct {
		q      string
		lights [agents.Keys]protocol.Light
	}{
		{"all six Agent Keys solid RED", all(red)},
		{"ONLY the top-left Agent Key green, the rest off", one},
		{"six different colors: red, orange, yellow, green, blue, purple (top row L-R, then second row L-R)", rainbow},
		{"all six BREATHING blue", all(protocol.Light{Effect: 4, Brightness: 1, Speed: 0.5, Color: 0x3B82F6})},
		{"all six DIM white (much dimmer than before)", all(protocol.Light{Effect: 1, Brightness: 0.2, Color: 0xFFFFFF})},
	}
	answers := map[string]string{}
	for i, p := range probes {
		if err := pad.Send(protocol.AgentKeys(p.lights)); err != nil {
			return err
		}
		say("[%d/%d] Do you see %s? y / n / d (describe) ", i+1, len(probes), p.q)
		var b byte
		for b = range typed {
			if b == 'y' || b == 'n' || b == 'd' || b == 3 {
				break
			}
		}
		if b == 3 {
			return errors.New("interrupted")
		}
		ans := string(b)
		if b == 'd' {
			say("\ndescribe what you see, then Enter: ")
			var line []byte
			for c := range typed {
				if c == '\r' || c == '\n' {
					break
				}
				if c == 127 && len(line) > 0 {
					line = line[:len(line)-1]
					say("\b \b")
					continue
				}
				line = append(line, c)
				fmt.Print(string(c))
			}
			ans = "d: " + string(line)
		}
		say("  %s\n", ans)
		answers[p.q] = ans
	}
	var dark [agents.Keys]protocol.Light
	_ = pad.Send(protocol.AgentKeys(dark))
	out, _ := json.MarshalIndent(answers, "", "  ")
	path := filepath.Join(filepath.Dir(testReportPath()), "light-probe.json")
	_ = os.WriteFile(path, out, 0o644)
	say("\nsaved to %s\n", path)
	return nil
}

func rawRPC(msgs []string) error {
	dev, err := hid.Open()
	if err != nil {
		return err
	}
	pad := micro.New(dev)
	defer pad.Close()
	pad.Raw = func(m protocol.Message) { fmt.Println("<-", string(m.Raw)) }
	select {
	case <-waitConnected(pad):
	case <-time.After(2 * time.Second):
		return errors.New("no Codex Micro found")
	}
	eol := os.Getenv("REX_MICRO_EOL")
	for _, m := range msgs {
		fmt.Println("->", m)
		payload := []byte(m)
		if eol == "lf" {
			// Frame appends CRLF; send LF instead by framing by hand.
			err = dev.Send(func(t string) [][]byte { return protocol.FrameEOL(payload, t, "\n") })
		} else {
			err = pad.Send(payload)
		}
		if err != nil {
			return err
		}
		time.Sleep(700 * time.Millisecond)
	}
	return nil
}
